package handler

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"logmara/model"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

// validJSONBKey ensures the field name is a safe JSONB key identifier
// (alphanumeric + underscore only). This prevents SQL injection via the
// parsed_fields->>'<field>' accessor, where the field name is embedded
// directly in the SQL string.
var validJSONBKey = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// filteredQueryTimeout bounds queries that carry user-supplied field filters
// (notably the "regex" operator, which runs as Postgres' backtracking `~` and
// could otherwise be driven into catastrophic runtime by a crafted pattern).
// lib/pq propagates context cancellation to the server as a query-cancel
// request, so this caps the actual database-side work, not just the HTTP wait.
// Deliberately generous so legitimate large scans still complete.
const filteredQueryTimeout = 20 * time.Second

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
	Hostname        string
	FromHostIP      string
	Severity        string
	AppName         string
	Search          string
	From            string
	To              string
	Devices         []string
	HasFields       bool
	RequiredParsers []string
	FieldFilters    []model.FieldFilter
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
		clauses = append(clauses, "(syslog_logs.fromhost_ip IS NULL OR syslog_logs.fromhost_ip = '')")
	} else if opts.FromHostIP != "" {
		clauses = append(clauses, fmt.Sprintf("COALESCE(syslog_logs.fromhost_ip, '') = $%d", idx))
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
		clauses = append(clauses, "COALESCE(syslog_logs.fromhost_ip, '') IN ("+strings.Join(placeholders, ", ")+")")
	}

	if opts.HasFields {
		clauses = append(clauses, "matched_parsers IS NOT NULL AND array_length(matched_parsers, 1) > 0")
	}

	// Restrict to rows actually matched by one of the parsers that own the
	// dashboard's selected fields - HasFields alone only requires "matched
	// by some parser", so a log parsed by an unrelated parser would
	// otherwise still show up with blank columns for every selected field.
	if len(opts.RequiredParsers) > 0 {
		clauses = append(clauses, fmt.Sprintf("matched_parsers && $%d::text[]", idx))
		args = append(args, pq.Array(opts.RequiredParsers))
		idx++
	}

		// Dynamic field filters for parsed_fields and static columns
		if len(opts.FieldFilters) > 0 {
			staticFields := map[string]bool{
				"hostname": true, "fromhost_ip": true, "severity": true,
				"app_name": true, "facility": true, "process_id": true,
				"msg_id": true, "message": true, "raw_message": true,
			}
			for _, ff := range opts.FieldFilters {
				// Reject any field name that isn't in the static whitelist
				// and doesn't match a safe JSONB key pattern.
				if !staticFields[ff.Field] && !validJSONBKey.MatchString(ff.Field) {
					continue
				}
				fieldCol := ff.Field
				if !staticFields[fieldCol] {
					fieldCol = fmt.Sprintf("parsed_fields->>'%s'", strings.ReplaceAll(ff.Field, "'", "''"))
				}
			op := normalizeOperator(ff.Operator)
			switch op {
			case "eq":
				if len(ff.Values) == 0 { continue }
				clauses = append(clauses, fmt.Sprintf("%s = $%d", fieldCol, idx))
				args = append(args, ff.Values[0])
				idx++
			case "neq":
				if len(ff.Values) == 0 { continue }
				clauses = append(clauses, fmt.Sprintf("%s <> $%d", fieldCol, idx))
				args = append(args, ff.Values[0])
				idx++
			case "contains":
				if len(ff.Values) == 0 { continue }
				clauses = append(clauses, fmt.Sprintf("%s ILIKE $%d", fieldCol, idx))
				args = append(args, "%"+ff.Values[0]+"%")
				idx++
			case "in":
				if len(ff.Values) == 0 { continue }
				placeholders := make([]string, len(ff.Values))
				for i, v := range ff.Values {
					placeholders[i] = fmt.Sprintf("$%d", idx)
					args = append(args, v)
					idx++
				}
				clauses = append(clauses, fmt.Sprintf("%s IN (%s)", fieldCol, strings.Join(placeholders, ", ")))
			case "notin":
				if len(ff.Values) == 0 { continue }
				placeholders := make([]string, len(ff.Values))
				for i, v := range ff.Values {
					placeholders[i] = fmt.Sprintf("$%d", idx)
					args = append(args, v)
					idx++
				}
				clauses = append(clauses, fmt.Sprintf("%s NOT IN (%s)", fieldCol, strings.Join(placeholders, ", ")))
			case "startswith":
				if len(ff.Values) == 0 { continue }
				clauses = append(clauses, fmt.Sprintf("%s ILIKE $%d", fieldCol, idx))
				args = append(args, ff.Values[0]+"%")
				idx++
			case "endswith":
				if len(ff.Values) == 0 { continue }
				clauses = append(clauses, fmt.Sprintf("%s ILIKE $%d", fieldCol, idx))
				args = append(args, "%"+ff.Values[0])
				idx++
			case "gt":
				if len(ff.Values) == 0 { continue }
				clauses = append(clauses, fmt.Sprintf("%s > $%d", fieldCol, idx))
				args = append(args, ff.Values[0])
				idx++
			case "gte":
				if len(ff.Values) == 0 { continue }
				clauses = append(clauses, fmt.Sprintf("%s >= $%d", fieldCol, idx))
				args = append(args, ff.Values[0])
				idx++
			case "lt":
				if len(ff.Values) == 0 { continue }
				clauses = append(clauses, fmt.Sprintf("%s < $%d", fieldCol, idx))
				args = append(args, ff.Values[0])
				idx++
			case "lte":
				if len(ff.Values) == 0 { continue }
				clauses = append(clauses, fmt.Sprintf("%s <= $%d", fieldCol, idx))
				args = append(args, ff.Values[0])
				idx++
			case "not_contains":
				if len(ff.Values) == 0 { continue }
				clauses = append(clauses, fmt.Sprintf("%s NOT ILIKE $%d", fieldCol, idx))
				args = append(args, "%"+ff.Values[0]+"%")
				idx++
			case "regex":
				if len(ff.Values) == 0 { continue }
				clauses = append(clauses, fmt.Sprintf("%s ~ $%d", fieldCol, idx))
				args = append(args, ff.Values[0])
				idx++
			case "is_empty":
				clauses = append(clauses, fmt.Sprintf("(%s IS NULL OR %s = '')", fieldCol, fieldCol))
			case "is_not_empty":
				clauses = append(clauses, fmt.Sprintf("(%s IS NOT NULL AND %s != '')", fieldCol, fieldCol))
			default:
				if len(ff.Values) == 0 {
					continue
				}
				clauses = append(clauses, fmt.Sprintf("%s = $%d", fieldCol, idx))
				args = append(args, ff.Values[0])
				idx++
			}
		}
	}

	return clauses, args, idx
}

func normalizeOperator(op string) string {
	if op == "" {
		return "eq"
	}
	switch op {
	case "not_in":
		return "notin"
	case "starts_with":
		return "startswith"
	case "ends_with":
		return "endswith"
	case "not_contains":
		return "not_contains"
	default:
		return op
	}
}

func buildWhereSQL(clauses []string) string {
	if len(clauses) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(clauses, " AND ")
}

// encodeLogCursor/decodeLogCursor implement keyset pagination on
// (timestamp, id). Unlike OFFSET, which forces Postgres to scan and discard
// every preceding row, a keyset cursor lets the planner seek directly via
// the (timestamp DESC, id DESC) index/order - lookup cost stays roughly
// constant no matter how deep into the log history the user scrolls.
func encodeLogCursor(ts time.Time, id int64) string {
	raw := fmt.Sprintf("%s|%d", ts.UTC().Format(time.RFC3339Nano), id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeLogCursor(cursor string) (time.Time, int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("invalid cursor encoding: %w", err)
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, 0, fmt.Errorf("invalid cursor format")
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("invalid cursor timestamp: %w", err)
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("invalid cursor id: %w", err)
	}
	return ts, id, nil
}

// cursorSupported reports whether the given sort mode can use keyset
// pagination. Keyset row-comparison ((a,b) < (x,y)) requires every column in
// the ORDER BY to be compared in the same direction, which holds for the
// two timestamp-only sorts but not for "severity"/"hostname" (ASC on the
// secondary column, DESC on timestamp) - those keep offset-based paging,
// which is acceptable since deep pagination on a secondary sort is rare.
func cursorSupported(sort string) bool {
	return sort == "" || sort == "timestamp_desc" || sort == "timestamp_asc"
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
			&l.Message, &l.RawMessage, &rawParsed, &parsers, &l.CreatedAt, &l.DisplayName,
		)
		if err != nil {
			continue
		}
		l.MatchedParsers = parsers
		if len(rawParsed) > 0 {
			if err := json.Unmarshal(rawParsed, &l.ParsedFields); err != nil {
				slog.Warn("failed to unmarshal parsed_fields", "log_id", l.ID, "error", err)
			}
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

func parsePaginationFromStrings(limitStr, offsetStr string, defaultLimit, maxLimit int) (int, int) {
	limitInt, _ := strconv.Atoi(limitStr)
	offsetInt, _ := strconv.Atoi(offsetStr)
	if limitInt <= 0 || limitInt > maxLimit {
		limitInt = defaultLimit
	}
	if offsetInt < 0 {
		offsetInt = 0
	}
	return limitInt, offsetInt
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
