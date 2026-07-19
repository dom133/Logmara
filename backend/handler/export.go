package handler

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"syslog-gui/middleware"
	"syslog-gui/model"

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
		writeCSVExport(c, db, buildWhereSQL(whereClauses), args, limit)
	}
}

func ExportHTML(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		whereClauses, args, _ := buildLogWhereClauses(exportFilterOptionsFromQuery(c))
		writeHTMLExport(c, db, buildWhereSQL(whereClauses), args, 5000)
	}
}

// writeCSVExport streams the filtered rows as CSV. Shared by the generic
// /export/csv endpoint and the dashboard-scoped export, which differ only in
// how whereSQL/args were built (see exportFilterOptionsFromQuery vs.
// resolveDashboardFilters).
func writeCSVExport(c *gin.Context, db *sql.DB, whereSQL string, args []interface{}, limit int) {
	rows, err := db.Query(
		fmt.Sprintf("SELECT timestamp, hostname, app_name, severity, message FROM syslog_logs %s ORDER BY timestamp DESC LIMIT $%d", whereSQL, len(args)+1),
		append(args, limit)...,
	)
	if err != nil {
		middleware.HandleError(c, model.NewInternal("Query failed", err))
		return
	}
	defer rows.Close()

	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", "attachment; filename=syslog_export_"+time.Now().Format("20060102_150405")+".csv")

	w := csv.NewWriter(c.Writer)
	w.Write([]string{"timestamp", "hostname", "app_name", "severity", "message"})

	for rows.Next() {
		var ts, hostname, severity, message string
		var appName *string
		if err := rows.Scan(&ts, &hostname, &appName, &severity, &message); err != nil {
			continue
		}
		app := ""
		if appName != nil {
			app = *appName
		}
		w.Write([]string{ts, csvSafe(hostname), csvSafe(app), csvSafe(severity), csvSafe(message)})
	}
	w.Flush()
}

// writeHTMLExport streams the filtered rows as an HTML report. See
// writeCSVExport for why this is shared between two callers.
func writeHTMLExport(c *gin.Context, db *sql.DB, whereSQL string, args []interface{}, limit int) {
	rows, err := db.Query(
		fmt.Sprintf("SELECT timestamp, hostname, app_name, severity, message FROM syslog_logs %s ORDER BY timestamp DESC LIMIT $%d", whereSQL, len(args)+1),
		append(args, limit)...,
	)
	if err != nil {
		middleware.HandleError(c, model.NewInternal("Query failed", err))
		return
	}
	defer rows.Close()

	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html><head><meta charset='utf-8'><title>Syslog Report</title>")
	sb.WriteString("<style>body{font-family:monospace;font-size:10px;}table{border-collapse:collapse;width:100%;}th,td{border:1px solid #ccc;padding:4px;text-align:left;}th{background:#2980b9;color:white;}tr:nth-child(even){background:#f0f0f0;}</style>")
	sb.WriteString("</head><body><h2>Syslog Report - " + time.Now().Format("2006-01-02 15:04:05") + "</h2>")
	sb.WriteString("<table><tr><th>Timestamp</th><th>Hostname</th><th>App</th><th>Severity</th><th>Message</th></tr>")

	for rows.Next() {
		var ts, hn, sev, msg string
		var app *string
		if err := rows.Scan(&ts, &hn, &app, &sev, &msg); err != nil {
			continue
		}
		a := ""
		if app != nil {
			a = *app
		}
		sb.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>",
			html.EscapeString(ts), html.EscapeString(hn), html.EscapeString(a), html.EscapeString(sev), html.EscapeString(msg)))
	}

	sb.WriteString("</table></body></html>")

	filename := "syslog_report_" + time.Now().Format("20060102_150405") + ".html"
	c.Header("Content-Type", "text/html")
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.String(http.StatusOK, sb.String())
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
