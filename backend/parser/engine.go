package parser

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"

	"logmara/db"
	"logmara/model"
)

type Engine struct {
	pool     *db.DynamicPool
	parsers  []model.Parser
	mu       sync.RWMutex
	reloadCh chan struct{}
}

// regexCache holds compiled regexes keyed by pattern source, shared across
// all Engine instances. Match/Extract run on every single ingested log
// line for every enabled parser - recompiling the same pattern per line
// (regexp.Compile does a full parse + NFA build) was the dominant per-line
// CPU cost at high ingestion rates. Keyed by pattern text rather than
// parser ID, so a reload that leaves a pattern unchanged keeps reusing its
// cached regex, and a changed pattern simply gets a new entry (old ones are
// harmless, bounded by the number of distinct patterns ever seen).
var regexCache sync.Map // map[string]*regexp.Regexp

func compileCached(pattern string) (*regexp.Regexp, error) {
	if v, ok := regexCache.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	regexCache.Store(pattern, re)
	return re, nil
}

func NewEngine(pool *db.DynamicPool) *Engine {
	e := &Engine{
		pool:     pool,
		reloadCh: make(chan struct{}, 1),
	}
	e.loadParsers()
	go e.runReloadLoop()
	return e
}

func (e *Engine) GetPool() *db.DynamicPool {
	return e.pool
}

func (e *Engine) loadParsers() {
	rows, err := e.pool.Get().Query(`
		SELECT id, name, description, device_type, match_type, match_value, regex, enabled, is_builtin, created_at, updated_at
		FROM parsers WHERE enabled = true ORDER BY id
	`)
	if err != nil {
		slog.Error("failed to load parsers", "error", err)
		return
	}
	defer rows.Close()

	var parsers []model.Parser
	for rows.Next() {
		var p model.Parser
		err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.DeviceType,
			&p.MatchType, &p.MatchValue, &p.Regex, &p.Enabled, &p.IsBuiltin, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			slog.Error("parser scan error", "error", err)
			continue
		}

		fieldRows, err := e.pool.Get().Query(`
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

	slog.Info("loaded enabled parsers", "count", len(parsers))
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

		re, err := compileCached(p.Regex)
		if err != nil {
			slog.Error("regex compile error", "parser", p.Name, "error", err)
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
	re, err := compileCached(globToRegex(pattern))
	if err != nil {
		return false
	}
	return re.MatchString(value)
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

	re, err := compileCached(parser.Regex)
	if err != nil {
		slog.Error("regex compile error", "parser", parser.Name, "error", err)
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
	Fields  map[string]string
	Parsers []string
}

func (e *Engine) Parse(hostname, appName, message string) *ParseResult {
	matched := e.Match(hostname, appName, message)
	if len(matched) == 0 {
		slog.Debug("no parser match", "hostname", hostname, "app", appName)
		return nil
	}

	merged := make(map[string]string)
	var parserNames []string
	for _, p := range matched {
		parserNames = append(parserNames, p.Name)
		fields := e.Extract(&p, message)
		for k, v := range fields {
			if _, exists := merged[k]; !exists {
				merged[k] = v
			}
		}
	}

	return &ParseResult{Fields: merged, Parsers: parserNames}
}

func (e *Engine) GetAllParsers() ([]model.Parser, error) {
	rows, err := e.pool.Get().Query(`
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

		fieldRows, err := e.pool.Get().Query(`
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
	rows, err := e.pool.Get().Query(`
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

	// Use the actual per-message match history (matched_parsers, populated at
		// ingest time) rather than re-matching a sample of messages live: a live
		// sample can miss infrequent message types (e.g. a WiFi-connect event
		// among thousands of other log lines) and wrongly report zero fields for
		// a parser the device genuinely has logs for.
		rows, err := e.pool.Get().Query(`
		SELECT DISTINCT elem
		FROM syslog_logs, unnest(matched_parsers) as elem
		WHERE COALESCE(fromhost_ip, '') = ANY($1)
	`, pq.Array(hostnames))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var parserNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		parserNames = append(parserNames, name)
	}

	if len(parserNames) == 0 {
		return []model.ParsedField{}, nil
	}

	fieldRows, err := e.pool.Get().Query(`
		SELECT f.id, f.parser_id, f.field_name, f.field_label, f.field_type, p.name as parser_name
		FROM parsed_fields_registry f
		JOIN parsers p ON f.parser_id = p.id
		WHERE p.name = ANY($1)
		ORDER BY p.device_type, f.field_name
	`, pq.Array(parserNames))
	if err != nil {
		return nil, err
	}
	defer fieldRows.Close()

	// Dedupe only within the same parser (parsed_fields_registry already
	// enforces UNIQUE(parser_id, field_name), so this just guards against
	// re-scanning the same row twice) - different parsers legitimately reuse
	// field names like "src_ip"/"mac_address", and keying on name alone would
	// silently drop, say, Ubiquiti Firewall Log's "src_ip" whenever some
	// other matched parser for the same device(s) also has a "src_ip" field.
	var fields []model.ParsedField
	seen := make(map[string]bool)
	for fieldRows.Next() {
		var f model.ParsedField
		if err := fieldRows.Scan(&f.ID, &f.ParserID, &f.Name, &f.Label, &f.Type, &f.ParserName); err != nil {
			continue
		}
		key := f.ParserName + "\x00" + f.Name
		if !seen[key] {
			seen[key] = true
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
		query += fmt.Sprintf(" AND COALESCE(fromhost_ip, '') = $%d", argIdx)
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

	rows, err := e.pool.Get().Query(query, args...)
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

		_, err = e.pool.Get().Exec(updateStmt, jsonData, pq.StringArray(result.Parsers), id)
		if err != nil {
			slog.Error("reparse update error", "error", err)
			continue
		}
		updated++

		if processed%1000 == 0 {
			slog.Info("reparse progress", "processed", processed, "updated", updated)
		}
	}

	resp := &model.ReparseResponse{Processed: processed, Updated: updated}
	slog.Info("reparse complete", "processed", processed, "updated", updated)
	return resp, nil
}
