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
		hostname := c.Query("hostname")
		severity := c.Query("severity")
		from := c.Query("from")
		to := c.Query("to")
		search := c.Query("search")
		limitStr := c.DefaultQuery("limit", "100000")
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit <= 0 {
			limit = DefaultExportLimit
		}
		if limit > MaxExportLimit {
			limit = MaxExportLimit
		}

		whereClauses := []string{}
		args := []interface{}{}
		argIdx := 1

		if hostname != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("hostname = $%d", argIdx))
			args = append(args, hostname)
			argIdx++
		}
		if severity != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("severity = $%d", argIdx))
			args = append(args, severity)
			argIdx++
		}
		if from != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("timestamp >= $%d", argIdx))
			args = append(args, from)
			argIdx++
		}
		if to != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("timestamp <= $%d", argIdx))
			args = append(args, to)
			argIdx++
		}
		if search != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("search_vector @@ websearch_to_tsquery('english', $%d)", argIdx))
			args = append(args, search)
			argIdx++
		}

		whereSQL := ""
		if len(whereClauses) > 0 {
			whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
		}

		rows, err := db.Query(
			"SELECT timestamp, hostname, app_name, severity, message FROM syslog_logs "+whereSQL+" ORDER BY timestamp DESC LIMIT $1",
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
}

func ExportHTML(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		hostname := c.Query("hostname")
		severity := c.Query("severity")
		from := c.Query("from")
		to := c.Query("to")
		search := c.Query("search")

		whereClauses := []string{}
		args := []interface{}{}
		argIdx := 1

		if hostname != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("hostname = $%d", argIdx))
			args = append(args, hostname)
			argIdx++
		}
		if severity != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("severity = $%d", argIdx))
			args = append(args, severity)
			argIdx++
		}
		if from != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("timestamp >= $%d", argIdx))
			args = append(args, from)
			argIdx++
		}
		if to != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("timestamp <= $%d", argIdx))
			args = append(args, to)
			argIdx++
		}
		if search != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("search_vector @@ websearch_to_tsquery('english', $%d)", argIdx))
			args = append(args, search)
			argIdx++
		}

		whereSQL := ""
		if len(whereClauses) > 0 {
			whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
		}

		rows, err := db.Query(
			"SELECT timestamp, hostname, app_name, severity, message FROM syslog_logs "+whereSQL+" ORDER BY timestamp DESC LIMIT 5000",
			args...,
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
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
