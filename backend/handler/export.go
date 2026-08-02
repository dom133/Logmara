package handler

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"logmara/middleware"
	"logmara/model"

	"github.com/gin-gonic/gin"
)

// ExportFilterRequest is the JSON body for export endpoints.
type ExportFilterRequest struct {
	Hostname   string `json:"hostname"`
	FromHostIP string `json:"fromhost_ip"`
	Severity   string `json:"severity"`
	AppName    string `json:"app_name"`
	Search     string `json:"search"`
	From       string `json:"from"`
	To         string `json:"to"`
	Devices    string `json:"devices"`
	HasFields  string `json:"has_fields"`
	TZ         string `json:"tz"`
	Limit      string `json:"limit"`
}

// exportFilterOptionsFromBody builds LogFilterOptions from the JSON request body.
func exportFilterOptionsFromBody(c *gin.Context) LogFilterOptions {
	var req ExportFilterRequest
	_ = c.ShouldBindJSON(&req)
	opts := LogFilterOptions{
		Hostname:   req.Hostname,
		FromHostIP: req.FromHostIP,
		Severity:   req.Severity,
		AppName:    req.AppName,
		Search:     req.Search,
		From:       req.From,
		To:         req.To,
	}
	if req.Devices != "" {
		opts.Devices = strings.Split(req.Devices, ",")
	}
	opts.HasFields = req.HasFields == "1"
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
		var req ExportFilterRequest
		_ = c.ShouldBindJSON(&req)
		limitStr := req.Limit
		if limitStr == "" {
			limitStr = "100000"
		}
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit <= 0 {
			limit = DefaultExportLimit
		}
		if limit > MaxExportLimit {
			limit = MaxExportLimit
		}

		whereClauses, args, _ := buildLogWhereClauses(exportFilterOptionsFromBody(c))
		writeCSVExport(c, db, buildWhereSQL(whereClauses), args, limit, nil, "Logs")
	}
}

func ExportHTML(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		whereClauses, args, _ := buildLogWhereClauses(exportFilterOptionsFromBody(c))
		writeHTMLExport(c, db, buildWhereSQL(whereClauses), args, 5000, nil, "Logs")
	}
}

// writeCSVExport streams the filtered rows as CSV. Shared by the generic
// /export/csv endpoint and the dashboard-scoped export, which differ only in
// how whereSQL/args were built (see exportFilterOptionsFromQuery vs.
// resolveDashboardFilters) and whether fields carries a dashboard's custom
// parsed-field columns (nil for the generic endpoint, which has none).
// sourceLabel identifies the export origin ("Logs" or dashboard name).
func writeCSVExport(c *gin.Context, db *sql.DB, whereSQL string, args []interface{}, limit int, fields []string, sourceLabel string) {
	rows, err := queryExportRows(c, db, whereSQL, args, limit)
	if err != nil {
		middleware.HandleError(c, model.NewInternalKey("error.queryFailed", "Query failed", err))
		return
	}
	defer rows.Close()

	sanitized := sanitizeFilename(sourceLabel)
	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", "attachment; filename=syslog_"+sanitized+"_"+time.Now().Format("20060102_150405")+".csv")

	w := csv.NewWriter(c.Writer)
	w.Write([]string{"# Export: " + sourceLabel})
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
// is for. sourceLabel identifies the export origin ("Logs" or dashboard name).
func writeHTMLExport(c *gin.Context, db *sql.DB, whereSQL string, args []interface{}, limit int, fields []string, sourceLabel string) {
	rows, err := queryExportRows(c, db, whereSQL, args, limit)
	if err != nil {
		middleware.HandleError(c, model.NewInternalKey("error.queryFailed", "Query failed", err))
		return
	}
	defer rows.Close()

	var rowCount int
	var rowsData []struct {
		ts, hostname, app, severity, message string
		parsed                              map[string]string
	}

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
		parsed := parseFieldsJSON(rawParsed)
		rowCount++
		rowsData = append(rowsData, struct {
			ts, hostname, app, severity, message string
			parsed                              map[string]string
		}{ts, hn, a, sev, msg, parsed})
	}

	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html>\n<html lang='en'>\n<head>\n")
	sb.WriteString("  <meta charset='utf-8'>\n")
	sb.WriteString("  <meta name='viewport' content='width=device-width, initial-scale=1.0'>\n")
	sb.WriteString("  <title>Logmara - Syslog Report - " + html.EscapeString(sourceLabel) + "</title>\n")
	sb.WriteString("  <style>\n")
	sb.WriteString("    * { margin: 0; padding: 0; box-sizing: border-box; }\n")
	sb.WriteString("    body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif; background: #f0f2f5; color: #333; }\n")
	sb.WriteString("    .header { background: linear-gradient(135deg, #1890ff 0%, #096dd9 100%); color: white; padding: 32px 40px; }\n")
	sb.WriteString("    .header-inner { max-width: 1200px; margin: 0 auto; display: flex; align-items: center; gap: 20px; }\n")
	sb.WriteString("    .logo { font-size: 28px; font-weight: 700; letter-spacing: -0.5px; display: flex; align-items: center; gap: 12px; }\n")
	sb.WriteString("    .logo-icon { width: 40px; height: 40px; background: rgba(255,255,255,0.2); border-radius: 8px; display: flex; align-items: center; justify-content: center; font-size: 22px; }\n")
	sb.WriteString("    .header-info { flex: 1; }\n")
	sb.WriteString("    .header-info h1 { font-size: 22px; font-weight: 600; margin-bottom: 4px; }\n")
	sb.WriteString("    .header-info p { font-size: 14px; opacity: 0.85; }\n")
	sb.WriteString("    .badge { display: inline-block; background: rgba(255,255,255,0.2); padding: 4px 12px; border-radius: 12px; font-size: 13px; margin-top: 8px; }\n")
	sb.WriteString("    .content { max-width: 1200px; margin: 24px auto; padding: 0 20px; }\n")
	sb.WriteString("    .stats { display: flex; gap: 16px; margin-bottom: 20px; flex-wrap: wrap; }\n")
	sb.WriteString("    .stat-card { background: white; border-radius: 8px; padding: 16px 24px; flex: 1; min-width: 180px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }\n")
	sb.WriteString("    .stat-card .label { font-size: 12px; color: #999; text-transform: uppercase; letter-spacing: 0.5px; }\n")
	sb.WriteString("    .stat-card .value { font-size: 20px; font-weight: 600; color: #1890ff; margin-top: 4px; }\n")
	sb.WriteString("    .table-wrap { background: white; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); overflow: hidden; }\n")
	sb.WriteString("    table { width: 100%; border-collapse: collapse; font-size: 13px; }\n")
	sb.WriteString("    thead { background: #fafafa; }\n")
	sb.WriteString("    th { padding: 12px 16px; text-align: left; font-weight: 600; color: #666; border-bottom: 2px solid #e8e8e8; white-space: nowrap; }\n")
	sb.WriteString("    td { padding: 10px 16px; border-bottom: 1px solid #f0f0f0; }\n")
	sb.WriteString("    tbody tr:hover { background: #e6f7ff; }\n")
	sb.WriteString("    tbody tr:nth-child(even) { background: #fafafa; }\n")
	sb.WriteString("    tbody tr:nth-child(even):hover { background: #e6f7ff; }\n")
	sb.WriteString("    .sev-emerg { color: #cf1322; font-weight: 600; }\n")
	sb.WriteString("    .sev-alert { color: #cf1322; }\n")
	sb.WriteString("    .sev-crit { color: #f5222d; }\n")
	sb.WriteString("    .sev-err { color: #f5222d; }\n")
	sb.WriteString("    .sev-warning { color: #fa8c16; }\n")
	sb.WriteString("    .sev-notice { color: #1890ff; }\n")
	sb.WriteString("    .sev-info { color: #3f8600; }\n")
	sb.WriteString("    .sev-debug { color: #999; }\n")
	sb.WriteString("    .footer { text-align: center; padding: 20px; color: #999; font-size: 12px; }\n")
	sb.WriteString("    @media print { body { background: white; } .header { background: #1890ff !important; -webkit-print-color-adjust: exact; print-color-adjust: exact; } .table-wrap { box-shadow: none; } tbody tr:nth-child(even) { background: #f9f9f9; } }\n")
	sb.WriteString("  </style>\n")
	sb.WriteString("</head>\n<body>\n")

	sb.WriteString("  <div class='header'>\n")
	sb.WriteString("    <div class='header-inner'>\n")
	sb.WriteString("      <div class='logo'><div class='logo-icon'>📋</div>Logmara</div>\n")
	sb.WriteString("      <div class='header-info'>\n")
	sb.WriteString("        <h1>Syslog Report</h1>\n")
	sb.WriteString("        <p>Generated: " + html.EscapeString(time.Now().Format("2006-01-02 15:04:05")) + "</p>\n")
	sb.WriteString("        <span class='badge'>Source: " + html.EscapeString(sourceLabel) + "</span>\n")
	sb.WriteString("      </div>\n")
	sb.WriteString("    </div>\n")
	sb.WriteString("  </div>\n")

	sb.WriteString("  <div class='content'>\n")
	sb.WriteString("    <div class='stats'>\n")
	sb.WriteString("      <div class='stat-card'><div class='label'>Total Entries</div><div class='value'>" + strconv.Itoa(rowCount) + "</div></div>\n")
	sb.WriteString("      <div class='stat-card'><div class='label'>Export Time</div><div class='value' style='font-size:14px'>" + html.EscapeString(time.Now().Format("15:04:05")) + "</div></div>\n")
	sb.WriteString("    </div>\n")

	sb.WriteString("    <div class='table-wrap'>\n")
	sb.WriteString("      <table>\n")
	sb.WriteString("        <thead><tr>\n")
	sb.WriteString("          <th>Timestamp</th><th>Hostname</th><th>App</th><th>Severity</th><th>Message</th>\n")
	for _, f := range fields {
		sb.WriteString("          <th>" + html.EscapeString(f) + "</th>\n")
	}
	sb.WriteString("        </tr></thead>\n")
	sb.WriteString("        <tbody>\n")

	for _, r := range rowsData {
		sevClass := "sev-" + html.EscapeString(r.severity)
		sb.WriteString("          <tr>\n")
		sb.WriteString("            <td style='white-space:nowrap'>" + html.EscapeString(r.ts) + "</td>\n")
		sb.WriteString("            <td>" + html.EscapeString(r.hostname) + "</td>\n")
		sb.WriteString("            <td>" + html.EscapeString(r.app) + "</td>\n")
		sb.WriteString("            <td><span class='" + sevClass + "'>" + html.EscapeString(r.severity) + "</span></td>\n")
		sb.WriteString("            <td style='max-width:400px;word-break:break-word'>" + html.EscapeString(r.message) + "</td>\n")
		if len(fields) > 0 {
			for _, f := range fields {
				sb.WriteString("            <td>" + html.EscapeString(r.parsed[f]) + "</td>\n")
			}
		}
		sb.WriteString("          </tr>\n")
	}

	sb.WriteString("        </tbody>\n")
	sb.WriteString("      </table>\n")
	sb.WriteString("    </div>\n")
	sb.WriteString("  </div>\n")

	sb.WriteString("  <div class='footer'>Logmara &mdash; Syslog Collector &amp; Analyzer</div>\n")
	sb.WriteString("</body>\n</html>")

	sanitized := sanitizeFilename(sourceLabel)
	filename := "syslog_" + sanitized + "_report_" + time.Now().Format("20060102_150405") + ".html"
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(sb.String()))
}

// queryExportRows runs the shared export SELECT, rendering the timestamp
// column as wall-clock time in the visitor's timezone (sent as the "tz"
// query param, same as the dashboard timeline - see GetTimelineStats)
// instead of the database server's timezone, so exported files show the same
// times the visitor sees on screen. Falls back to UTC and retries once if
// Postgres rejects the zone name (unknown zones only fail at AT TIME ZONE
// evaluation, not query parse time).
func queryExportRows(c *gin.Context, db *sql.DB, whereSQL string, args []interface{}, limit int) (*sql.Rows, error) {
	var req ExportFilterRequest
	_ = c.ShouldBindJSON(&req)
	tz := req.TZ
	if tz == "" {
		tz = "UTC"
	}
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
		slog.Warn("export: timezone rejected by Postgres, falling back to UTC", "tz", tz, "error", err)
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
