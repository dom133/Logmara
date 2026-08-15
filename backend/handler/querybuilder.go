package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"syslog-gui/model"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

func timedQuery(name string, fn func() error) error {
	start := time.Now()
	err := fn()
	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		slog.Warn("slow query", "name", name, "duration_ms", elapsed.Milliseconds())
		recordSlowQuery(name, elapsed)
	}
	return err
}

type LogFilterOptions struct {
	Hostname   string
	FromHostIP string
	Severity   string
	AppName    string
	Search     string
	From       string
	To         string
	Devices    []string
	HasFields  bool
}

func buildLogWhereClauses(opts LogFilterOptions) ([]string, []interface{}, int) {
	clauses := []string{}
	args := []interface{}{}
	idx := 1

	if opts.Hostname != "" {
		clauses = append(clauses, fmt.Sprintf("hostname = $%d", idx))
		args = append(args, opts.Hostname)
		idx++
	}

	if opts.FromHostIP == "__unknown__" {
		clauses = append(clauses, "(fromhost_ip IS NULL OR fromhost_ip = '')")
	} else if opts.FromHostIP != "" {
		clauses = append(clauses, fmt.Sprintf("COALESCE(fromhost_ip, '') = $%d", idx))
		args = append(args, opts.FromHostIP)
		idx++
	}

	if opts.Severity != "" {
		clauses = append(clauses, fmt.Sprintf("severity = $%d", idx))
		args = append(args, opts.Severity)
		idx++
	}

	if opts.AppName != "" {
		clauses = append(clauses, fmt.Sprintf("app_name ILIKE $%d", idx))
		args = append(args, "%"+opts.AppName+"%")
		idx++
	}

	if opts.From != "" {
		clauses = append(clauses, fmt.Sprintf("timestamp >= $%d", idx))
		args = append(args, opts.From)
		idx++
	}

	if opts.To != "" {
		clauses = append(clauses, fmt.Sprintf("timestamp <= $%d", idx))
		args = append(args, opts.To)
		idx++
	}

	if opts.Search != "" {
		clauses = append(clauses, fmt.Sprintf("search_vector @@ websearch_to_tsquery('english', $%d)", idx))
		args = append(args, opts.Search)
		idx++
	}

	if len(opts.Devices) > 0 {
		placeholders := make([]string, len(opts.Devices))
		for i, d := range opts.Devices {
			placeholders[i] = "$" + strconv.Itoa(idx)
			args = append(args, d)
			idx++
		}
		clauses = append(clauses, "COALESCE(fromhost_ip, '') IN ("+strings.Join(placeholders, ", ")+")")
	}

	if opts.HasFields {
		clauses = append(clauses, "matched_parsers IS NOT NULL AND array_length(matched_parsers, 1) > 0")
	}

	return clauses, args, idx
}

func buildWhereSQL(clauses []string) string {
	if len(clauses) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(clauses, " AND ")
}

func scanLogRows(rows *sql.Rows) []model.SyslogLog {
	var logs []model.SyslogLog
	for rows.Next() {
		var l model.SyslogLog
		var rawParsed json.RawMessage
		var parsers pq.StringArray
		err := rows.Scan(
			&l.ID, &l.Timestamp, &l.Hostname, &l.FromHostIP, &l.AppName,
			&l.ProcessID, &l.MsgID, &l.Severity, &l.Facility,
			&l.Message, &l.RawMessage, &rawParsed, &parsers, &l.CreatedAt,
		)
		if err != nil {
			continue
		}
		l.MatchedParsers = parsers
		if len(rawParsed) > 0 {
			json.Unmarshal(rawParsed, &l.ParsedFields)
		}
		logs = append(logs, l)
	}
	if logs == nil {
		logs = []model.SyslogLog{}
	}
	return logs
}

func getDashboardConfig(db *sql.DB, id, userID int64, isAdmin bool) (json.RawMessage, error) {
	var raw json.RawMessage
	if isAdmin {
		return raw, db.QueryRow("SELECT config FROM dashboards WHERE id = $1", id).Scan(&raw)
	}
	return raw, db.QueryRow("SELECT config FROM dashboards WHERE id = $1 AND (owner_id = $2 OR is_public = TRUE)", id, userID).Scan(&raw)
}

func parseDashboardConfig(raw json.RawMessage) (*model.DashboardConfig, error) {
	var cfg model.DashboardConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func parsePagination(c *gin.Context, defaultLimit, maxLimit int) (int, int) {
	limitInt, _ := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(defaultLimit)))
	offsetInt, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limitInt <= 0 || limitInt > maxLimit {
		limitInt = defaultLimit
	}
	return limitInt, offsetInt
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
