package handler

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"syslytics/middleware"
	"syslytics/model"

	"github.com/gin-gonic/gin"
)

// exportFilterOptionsFromQuery builds LogFilterOptions from the query params
// shared by the generic /export endpoints and the dashboard-scoped ones -
// same filters the table view itself uses, so exports match what's on screen.
func exportFilterOptionsFromQuery(c *gin.Context) LogFilterOptions {
	opts := LogFilterOptions{
		Hostname:   c.Query("hostname"),
		FromHostIP: c.Query("fromhost_ip"),
		Severity:   c.Query("severity"),
		AppName:    c.Query("app_name"),
		Search:     c.Query("search"),
		From:       c.Query("from"),
		To:         c.Query("to"),
	}
	if devices := c.Query("devices"); devices != "" {
		opts.Devices = strings.Split(devices, ",")
	}
	opts.HasFields = c.Query("has_fields") == "1"
	return opts
}

// csvSafe neutralizes leading characters that spreadsheet applications
// (Excel, LibreOffice) interpret as the start of a formula, preventing
// CSV/formula injection from attacker-influenced log content.
func csvSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

func ExportCSV(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		limitStr := c.DefaultQuery("limit", "100000")
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit <= 0 {
			limit = DefaultExportLimit
		}
		if limit > MaxExportLimit {
			limit = MaxExportLimit
		}

		whereClauses, args, _ := buildLogWhereClauses(exportFilterOptionsFromQuery(c))
		writeCSVExport(c, db, buildWhereSQL(whereClauses), args, limit, nil)
	}
}

func ExportHTML(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		whereClauses, args, _ := buildLogWhereClauses(exportFilterOptionsFromQuery(c))
		writeHTMLExport(c, db, buildWhereSQL(whereClauses), args, 5000, nil)
	}
}

// writeCSVExport streams the filtered rows as CSV. Shared by the generic
// /export/csv endpoint and the dashboard-scoped export, which differ only in
// how whereSQL/args were built (see exportFilterOptionsFromQuery vs.
// resolveDashboardFilters) and whether fields carries a dashboard's custom
// parsed-field columns (nil for the generic endpoint, which has none).
func writeCSVExport(c *gin.Context, db *sql.DB, whereSQL string, args []interface{}, limit int, fields []string) {
	rows, err := queryExportRows(c, db, whereSQL, args, limit)
	if err != nil {
		middleware.HandleError(c, model.NewInternal("Query failed", err))
		return
	}
	defer rows.Close()

	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", "attachment; filename=syslog_export_"+time.Now().Format("20060102_150405")+".csv")

	w := csv.NewWriter(c.Writer)
	w.Write(append([]string{"timestamp", "hostname", "app_name", "severity", "message"}, fields...))

	for rows.Next() {
		var ts, hostname, severity, message string
		var appName *string
		var rawParsed []byte
		if err := rows.Scan(&ts, &hostname, &appName, &severity, &message, &rawParsed); err != nil {
			continue
		}
		app := ""
		if appName != nil {
			app = *appName
		}
		row := []string{ts, csvSafe(hostname), csvSafe(app), csvSafe(severity), csvSafe(message)}
		if len(fields) > 0 {
			parsed := parseFieldsJSON(rawParsed)
			for _, f := range fields {
				row = append(row, csvSafe(parsed[f]))
			}
		}
		w.Write(row)
	}
	w.Flush()
}

// writeHTMLExport streams the filtered rows as an HTML report. See
// writeCSVExport for why this is shared between two callers and what fields
// is for.
func writeHTMLExport(c *gin.Context, db *sql.DB, whereSQL string, args []interface{}, limit int, fields []string) {
	rows, err := queryExportRows(c, db, whereSQL, args, limit)
	if err != nil {
		middleware.HandleError(c, model.NewInternal("Query failed", err))
		return
	}
	defer rows.Close()

	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html><head><meta charset='utf-8'><title>Syslog Report</title>")
	sb.WriteString("<style>body{font-family:monospace;font-size:10px;}table{border-collapse:collapse;width:100%;}th,td{border:1px solid #ccc;padding:4px;text-align:left;}th{background:#2980b9;color:white;}tr:nth-child(even){background:#f0f0f0;}</style>")
	sb.WriteString("</head><body><h2>Syslog Report - " + time.Now().Format("2006-01-02 15:04:05") + "</h2>")
	sb.WriteString("<table><tr><th>Timestamp</th><th>Hostname</th><th>App</th><th>Severity</th><th>Message</th>")
	for _, f := range fields {
		sb.WriteString("<th>" + html.EscapeString(f) + "</th>")
	}
	sb.WriteString("</tr>")

	for rows.Next() {
		var ts, hn, sev, msg string
		var app *string
		var rawParsed []byte
		if err := rows.Scan(&ts, &hn, &app, &sev, &msg, &rawParsed); err != nil {
			continue
		}
		a := ""
		if app != nil {
			a = *app
		}
		sb.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td>",
			html.EscapeString(ts), html.EscapeString(hn), html.EscapeString(a), html.EscapeString(sev), html.EscapeString(msg)))
		if len(fields) > 0 {
			parsed := parseFieldsJSON(rawParsed)
			for _, f := range fields {
				sb.WriteString("<td>" + html.EscapeString(parsed[f]) + "</td>")
			}
		}
		sb.WriteString("</tr>")
	}

	sb.WriteString("</table></body></html>")

	filename := "syslog_report_" + time.Now().Format("20060102_150405") + ".html"
	c.Header("Content-Type", "text/html")
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.String(http.StatusOK, sb.String())
}

// queryExportRows runs the shared export SELECT, rendering the timestamp
// column as wall-clock time in the visitor's timezone (sent as the "tz"
// query param, same as the dashboard timeline - see GetTimelineStats)
// instead of the database server's timezone, so exported files show the same
// times the visitor sees on screen. Falls back to UTC and retries once if
// Postgres rejects the zone name (unknown zones only fail at AT TIME ZONE
// evaluation, not query parse time).
func queryExportRows(c *gin.Context, db *sql.DB, whereSQL string, args []interface{}, limit int) (*sql.Rows, error) {
	tz := c.DefaultQuery("tz", "UTC")
	tzIdx := len(args) + 1
	limitIdx := len(args) + 2
	query := fmt.Sprintf(
		"SELECT to_char(timestamp AT TIME ZONE $%d, 'YYYY-MM-DD HH24:MI:SS') as ts, hostname, app_name, severity, message, parsed_fields FROM syslog_logs %s ORDER BY timestamp DESC LIMIT $%d",
		tzIdx, whereSQL, limitIdx,
	)

	runQuery := func(tzVal string) (*sql.Rows, error) {
		fullArgs := make([]interface{}, 0, len(args)+2)
		fullArgs = append(fullArgs, args...)
		fullArgs = append(fullArgs, tzVal, limit)
		return db.Query(query, fullArgs...)
	}

	rows, err := runQuery(tz)
	if err != nil && tz != "UTC" {
		rows, err = runQuery("UTC")
	}
	return rows, err
}

// parseFieldsJSON decodes a syslog_logs.parsed_fields jsonb column into a
// string map, tolerating NULL/empty/malformed values (best-effort - a
// dashboard's custom-field columns just come back blank for that row).
func parseFieldsJSON(raw []byte) map[string]string {
	parsed := map[string]string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &parsed)
	}
	return parsed
}
