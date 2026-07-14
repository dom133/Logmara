package parser

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"syslog-gui/model"
)

type Engine struct {
	db       *sql.DB
	parsers  []model.Parser
	mu       sync.RWMutex
	reloadCh chan struct{}
}

func NewEngine(db *sql.DB) *Engine {
	e := &Engine{
		db:       db,
		reloadCh: make(chan struct{}, 1),
	}
	e.loadParsers()
	go e.runReloadLoop()
	return e
}

func (e *Engine) GetDB() *sql.DB {
	return e.db
}

func (e *Engine) loadParsers() {
	rows, err := e.db.Query(`
		SELECT id, name, description, device_type, match_type, match_value, regex, enabled, is_builtin, created_at, updated_at
		FROM parsers WHERE enabled = true ORDER BY id
	`)
	if err != nil {
		log.Printf("Parser: failed to load parsers: %v", err)
		return
	}
	defer rows.Close()

	var parsers []model.Parser
	for rows.Next() {
		var p model.Parser
		err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.DeviceType,
			&p.MatchType, &p.MatchValue, &p.Regex, &p.Enabled, &p.IsBuiltin, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			log.Printf("Parser: scan error: %v", err)
			continue
		}

		fieldRows, err := e.db.Query(`
			SELECT id, parser_id, field_name, field_label, field_type
			FROM parsed_fields_registry WHERE parser_id = $1 ORDER BY id
		`, p.ID)
		if err == nil {
			for fieldRows.Next() {
				var f model.ParsedField
				if err := fieldRows.Scan(&f.ID, &f.ParserID, &f.Name, &f.Label, &f.Type); err == nil {
					p.Fields = append(p.Fields, f)
				}
			}
			fieldRows.Close()
		}

		parsers = append(parsers, p)
	}

	e.mu.Lock()
	e.parsers = parsers
	e.mu.Unlock()

	log.Printf("Parser: loaded %d enabled parsers", len(parsers))
}

func (e *Engine) runReloadLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			e.loadParsers()
		case <-e.reloadCh:
			e.loadParsers()
		}
	}
}

func (e *Engine) Reload() {
	select {
	case e.reloadCh <- struct{}{}:
	default:
	}
}

func (e *Engine) Match(hostname, appName, message string) []model.Parser {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var matched []model.Parser
	for _, p := range e.parsers {
		if !e.parserMatches(&p, hostname, appName, message) {
			continue
		}

		re, err := regexp.Compile(p.Regex)
		if err != nil {
			continue
		}

		if re.MatchString(message) {
			matched = append(matched, p)
		}
	}

	return matched
}

func (e *Engine) parserMatches(p *model.Parser, hostname, appName, message string) bool {
	switch p.MatchType {
	case "hostname":
		if p.MatchValue == nil || *p.MatchValue == "" {
			return false
		}
		return matchGlob(*p.MatchValue, hostname)
	case "app_name":
		if p.MatchValue == nil || *p.MatchValue == "" {
			return false
		}
		if appName == "" {
			return false
		}
		return matchGlob(*p.MatchValue, appName)
	case "message":
		if p.MatchValue == nil || *p.MatchValue == "" {
			return false
		}
		return strings.Contains(message, *p.MatchValue)
	case "all":
		return true
	default:
		return false
	}
}

func matchGlob(pattern, value string) bool {
	re := globToRegex(pattern)
	matched, _ := regexp.MatchString(re, value)
	return matched
}

func globToRegex(pattern string) string {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		if r == '*' {
			b.WriteString(".*")
		} else if r == '?' {
			b.WriteString(".")
		} else if r == '.' || r == '+' || r == '(' || r == ')' ||
			r == '[' || r == ']' || r == '{' || r == '}' ||
			r == '^' || r == '$' || r == '|' || r == '\\' {
			b.WriteString("\\")
			b.WriteRune(r)
		} else {
			b.WriteRune(r)
		}
	}
	b.WriteString("$")
	return b.String()
}

func (e *Engine) Extract(parser *model.Parser, message string) map[string]string {
	result := make(map[string]string)

	re, err := regexp.Compile(parser.Regex)
	if err != nil {
		log.Printf("Parser: compile error for %s: %v", parser.Name, err)
		return nil
	}

	matches := re.FindStringSubmatch(message)
	if matches == nil {
		return nil
	}

	for i, field := range parser.Fields {
		if i+1 < len(matches) {
			result[field.Name] = matches[i+1]
		}
	}

	return result
}

type ParseResult struct {
	Fields    map[string]string
	Parsers   []string
}

func (e *Engine) Parse(hostname, appName, message string) *ParseResult {
	matched := e.Match(hostname, appName, message)
	if len(matched) == 0 {
		log.Printf("Parser: no match for hostname=%s app=%s msg=%.80s", hostname, appName, message)
		return nil
	}

	merged := make(map[string]string)
	var parserNames []string
	for _, p := range matched {
		parserNames = append(parserNames, p.Name)
		fields := e.Extract(&p, message)
		if fields != nil {
			for k, v := range fields {
				if _, exists := merged[k]; !exists {
					merged[k] = v
				}
			}
		}
	}

	log.Printf("Parser: matched %d parsers for hostname=%s app=%s -> %v (%v)", len(matched), hostname, appName, merged, parserNames)
	return &ParseResult{Fields: merged, Parsers: parserNames}
}

func (e *Engine) GetAllParsers() ([]model.Parser, error) {
	rows, err := e.db.Query(`
		SELECT id, name, description, device_type, match_type, match_value, regex, enabled, is_builtin, created_at, updated_at
		FROM parsers ORDER BY device_type, id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var parsers []model.Parser
	for rows.Next() {
		var p model.Parser
		err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.DeviceType,
			&p.MatchType, &p.MatchValue, &p.Regex, &p.Enabled, &p.IsBuiltin, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			continue
		}

		fieldRows, err := e.db.Query(`
			SELECT id, parser_id, field_name, field_label, field_type
			FROM parsed_fields_registry WHERE parser_id = $1 ORDER BY id
		`, p.ID)
		if err == nil {
			for fieldRows.Next() {
				var f model.ParsedField
				if err := fieldRows.Scan(&f.ID, &f.ParserID, &f.Name, &f.Label, &f.Type); err == nil {
					p.Fields = append(p.Fields, f)
				}
			}
			fieldRows.Close()
		}

		parsers = append(parsers, p)
	}

	return parsers, nil
}

func (e *Engine) GetParsedFieldRegistry() ([]model.ParsedField, error) {
	rows, err := e.db.Query(`
		SELECT f.id, f.parser_id, f.field_name, f.field_label, f.field_type, p.name as parser_name
		FROM parsed_fields_registry f
		JOIN parsers p ON f.parser_id = p.id
		ORDER BY p.device_type, f.field_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fields []model.ParsedField
	for rows.Next() {
		var f model.ParsedField
		if err := rows.Scan(&f.ID, &f.ParserID, &f.Name, &f.Label, &f.Type, &f.ParserName); err != nil {
			continue
		}
		fields = append(fields, f)
	}

	return fields, nil
}

func (e *Engine) GetParsedFieldsForHostnames(hostnames []string) ([]model.ParsedField, error) {
	if len(hostnames) == 0 {
		return e.GetParsedFieldRegistry()
	}

	rows, err := e.db.Query(`
		SELECT id, name, description, device_type, match_type, match_value, regex, enabled, is_builtin, created_at, updated_at
		FROM parsers
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var parsers []model.Parser
	for rows.Next() {
		var p model.Parser
		err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.DeviceType,
			&p.MatchType, &p.MatchValue, &p.Regex, &p.Enabled, &p.IsBuiltin, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			continue
		}
		parsers = append(parsers, p)
	}

	parserIDs := make(map[int64]bool)

	for _, hostname := range hostnames {
		appRows, err := e.db.Query(`SELECT DISTINCT app_name FROM syslog_logs WHERE hostname = $1`, hostname)
		if err != nil {
			continue
		}

		var appNames []string
		hasLogs := false
		for appRows.Next() {
			var appName sql.NullString
			if err := appRows.Scan(&appName); err != nil {
				continue
			}
			if appName.Valid {
				appNames = append(appNames, appName.String)
				hasLogs = true
			}
		}
		appRows.Close()

		if !hasLogs {
			continue
		}

		for _, p := range parsers {
			switch p.MatchType {
			case "all":
				parserIDs[p.ID] = true
			case "hostname":
				if p.MatchValue != nil && *p.MatchValue != "" && matchGlob(*p.MatchValue, hostname) {
					parserIDs[p.ID] = true
				}
			case "app_name":
				if p.MatchValue != nil && *p.MatchValue != "" {
					for _, appName := range appNames {
						if matchGlob(*p.MatchValue, appName) {
							parserIDs[p.ID] = true
							break
						}
					}
				}
			}
		}
	}

	if len(parserIDs) == 0 {
		return []model.ParsedField{}, nil
	}

	ids := make([]int64, 0, len(parserIDs))
	for id := range parserIDs {
		ids = append(ids, id)
	}

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT f.id, f.parser_id, f.field_name, f.field_label, f.field_type, p.name as parser_name
		FROM parsed_fields_registry f
		JOIN parsers p ON f.parser_id = p.id
		WHERE f.parser_id IN (%s)
		ORDER BY p.device_type, f.field_name
	`, strings.Join(placeholders, ", "))

	rows, err = e.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fields []model.ParsedField
	seen := make(map[string]bool)
	for rows.Next() {
		var f model.ParsedField
		if err := rows.Scan(&f.ID, &f.ParserID, &f.Name, &f.Label, &f.Type, &f.ParserName); err != nil {
			continue
		}
		if !seen[f.Name] {
			seen[f.Name] = true
			fields = append(fields, f)
		}
	}

	return fields, nil
}

func (e *Engine) TestParser(pattern string, sampleLog string) (*model.ParserTestResponse, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return &model.ParserTestResponse{Matched: false, Fields: nil}, fmt.Errorf("invalid regex: %w", err)
	}

	matches := re.FindStringSubmatch(sampleLog)
	if len(matches) <= 1 {
		return &model.ParserTestResponse{Matched: false, Fields: nil}, nil
	}

	fields := make(map[string]string)
	for i, v := range matches[1:] {
		fields[fmt.Sprintf("group_%d", i)] = v
	}

	return &model.ParserTestResponse{Matched: true, Fields: fields}, nil
}

func (e *Engine) ReparseUnparsed(hostname, from, to string, limit int) (*model.ReparseResponse, error) {
	query := `SELECT id, hostname, app_name, message FROM syslog_logs
		WHERE parsed_fields IS NULL OR parsed_fields = '{}'`

	args := []interface{}{}
	argIdx := 1

	if hostname != "" {
		query += fmt.Sprintf(" AND hostname = $%d", argIdx)
		args = append(args, hostname)
		argIdx++
	}
	if from != "" {
		query += fmt.Sprintf(" AND timestamp >= $%d", argIdx)
		args = append(args, from)
		argIdx++
	}
	if to != "" {
		query += fmt.Sprintf(" AND timestamp <= $%d", argIdx)
		args = append(args, to)
		argIdx++
	}
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, limit)
	} else {
		query += " LIMIT 10000"
	}

	rows, err := e.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	processed := 0
	updated := 0

	updateStmt := `UPDATE syslog_logs SET parsed_fields = $1, matched_parsers = $2 WHERE id = $3`

	for rows.Next() {
		var id int64
		var h, appName, message string
		var appPtr *string
		err := rows.Scan(&id, &h, &appPtr, &message)
		if err != nil {
			continue
		}
		processed++

		appName = ""
		if appPtr != nil {
			appName = *appPtr
		}

		result := e.Parse(h, appName, message)
		if result == nil {
			continue
		}

		jsonData, err := json.Marshal(result.Fields)
		if err != nil {
			continue
		}

		_, err = e.db.Exec(updateStmt, jsonData, result.Parsers, id)
		if err != nil {
			log.Printf("Parser: reparse update error: %v", err)
			continue
		}
		updated++

		if processed%1000 == 0 {
			log.Printf("Parser: reparsed %d/%d (updated %d)", processed, processed, updated)
		}
	}

	resp := &model.ReparseResponse{Processed: processed, Updated: updated}
	log.Printf("Parser: reparse complete: processed=%d, updated=%d", processed, updated)
	return resp, nil
}