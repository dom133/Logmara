package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"logmara/middleware"
	"logmara/model"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

type APIExportRequest struct {
	Hostname   string `json:"hostname"`
	FromHostIP string `json:"fromhost_ip"`
	Severity   string `json:"severity"`
	AppName    string `json:"app_name"`
	Search     string `json:"search"`
	From       string `json:"from"`
	To         string `json:"to"`
	Limit      string `json:"limit"`
	Cursor     string `json:"cursor"`
	TZ         string `json:"tz"`
}

func ExportJSON(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !middleware.CheckPermission(c, "export_json") {
			c.AbortWithError(http.StatusForbidden, model.NewForbiddenKey("forbidden", "Missing permission: export_json", nil))
			return
		}

		var req APIExportRequest
		_ = c.ShouldBindJSON(&req)

		limit, _ := strconv.Atoi(req.Limit)
		if limit <= 0 || limit > 10000 {
			limit = 1000
		}

		opts := LogFilterOptions{
			Hostname:   req.Hostname,
			FromHostIP: req.FromHostIP,
			Severity:   req.Severity,
			AppName:    req.AppName,
			Search:     req.Search,
			From:       req.From,
			To:         req.To,
		}

		whereClauses, args, _ := buildLogWhereClauses(opts)
		whereSQL := buildWhereSQL(whereClauses)

		scopeQuery, scopeArgs := middleware.ApplyScopeFilters(c, whereSQL, &args)
		if scopeQuery != "" {
			whereSQL = scopeQuery
			args = scopeArgs
		}

		tz := req.TZ
		if tz == "" {
			tz = "UTC"
		}

		logs, hasMore, nextCursor := queryLogs(database, whereSQL, args, limit, req.Cursor, tz)

		c.JSON(http.StatusOK, gin.H{
			"logs":       logs,
			"has_more":   hasMore,
			"next_cursor": nextCursor,
		})
	}
}

func ExportParsedJSON(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !middleware.CheckPermission(c, "export_parsed") {
			c.AbortWithError(http.StatusForbidden, model.NewForbiddenKey("forbidden", "Missing permission: export_parsed", nil))
			return
		}

		var req APIExportRequest
		_ = c.ShouldBindJSON(&req)

		limit, _ := strconv.Atoi(req.Limit)
		if limit <= 0 || limit > 10000 {
			limit = 1000
		}

		opts := LogFilterOptions{
			Hostname:   req.Hostname,
			FromHostIP: req.FromHostIP,
			Severity:   req.Severity,
			AppName:    req.AppName,
			Search:     req.Search,
			From:       req.From,
			To:         req.To,
			HasFields:  true,
		}

		whereClauses, args, _ := buildLogWhereClauses(opts)
		whereSQL := buildWhereSQL(whereClauses)

		scopeQuery, scopeArgs := middleware.ApplyScopeFilters(c, whereSQL, &args)
		if scopeQuery != "" {
			whereSQL = scopeQuery
			args = scopeArgs
		}

		tz := req.TZ
		if tz == "" {
			tz = "UTC"
		}

		logs, hasMore, nextCursor := queryLogs(database, whereSQL, args, limit, req.Cursor, tz)

		c.JSON(http.StatusOK, gin.H{
			"logs":       logs,
			"has_more":   hasMore,
			"next_cursor": nextCursor,
		})
	}
}

func ExportStats(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !middleware.CheckPermission(c, "view_stats") {
			c.AbortWithError(http.StatusForbidden, model.NewForbiddenKey("forbidden", "Missing permission: view_stats", nil))
			return
		}

		from := c.DefaultQuery("from", "")
		to := c.DefaultQuery("to", "")

		whereClauses := []string{}
		args := []interface{}{}
		idx := 1

		if from != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("timestamp >= $%d", idx))
			args = append(args, from)
			idx++
		}
		if to != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("timestamp <= $%d", idx))
			args = append(args, to)
			idx++
		}

		whereSQL := buildWhereSQL(whereClauses)

		scopeQuery, scopeArgs := middleware.ApplyScopeFilters(c, whereSQL, &args)
		if scopeQuery != "" {
			whereSQL = scopeQuery
			args = scopeArgs
		}

		var totalLogs int
		var err error
		if whereSQL != "" {
			err = database.QueryRow("SELECT COUNT(*) FROM syslog_logs "+whereSQL, args...).Scan(&totalLogs)
		} else {
			err = database.QueryRow("SELECT COUNT(*) FROM syslog_logs").Scan(&totalLogs)
		}
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, model.NewInternal("Query failed", err))
			return
		}

		var severityCounts []gin.H
		rows, err := database.Query("SELECT severity, COUNT(*) FROM syslog_logs "+whereSQL+" GROUP BY severity ORDER BY COUNT(*) DESC", args...)
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, model.NewInternal("Query failed", err))
			return
		}
		defer rows.Close()

		for rows.Next() {
			var severity string
			var count int
			rows.Scan(&severity, &count)
			severityCounts = append(severityCounts, gin.H{
				"severity": severity,
				"count":    count,
			})
		}

		var hostCounts []gin.H
		hostRows, err := database.Query("SELECT hostname, COUNT(*) FROM syslog_logs "+whereSQL+" GROUP BY hostname ORDER BY COUNT(*) DESC LIMIT 20", args...)
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, model.NewInternal("Query failed", err))
			return
		}
		defer hostRows.Close()

		for hostRows.Next() {
			var hostname string
			var count int
			hostRows.Scan(&hostname, &count)
			hostCounts = append(hostCounts, gin.H{
				"hostname": hostname,
				"count":    count,
			})
		}

		c.JSON(http.StatusOK, gin.H{
			"total_logs":     totalLogs,
			"severity_counts": severityCounts,
			"top_hosts":      hostCounts,
		})
	}
}

func queryLogs(database *sql.DB, whereSQL string, args []interface{}, limit int, cursor string, tz string) ([]gin.H, bool, string) {
	var logs []gin.H
	var hasMore bool
	var nextCursor string

	if cursor != "" {
		ts, id, err := decodeLogCursor(cursor)
		if err != nil {
			return []gin.H{}, false, ""
		}

		cursorIdx := len(args) + 1
		tzIdx := cursorIdx + 1
		tsCursorIdx := cursorIdx + 2
		idCursorIdx := cursorIdx + 3
		limitIdx := cursorIdx + 4

		query := fmt.Sprintf(
			"SELECT to_char((timestamp AT TIME ZONE $%d) AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS') as ts, id, hostname, fromhost_ip, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, matched_parsers, extract(epoch from timestamp) as ts_epoch FROM syslog_logs %s AND (timestamp < $%d OR (timestamp = $%d AND id < $%d)) ORDER BY timestamp DESC, id DESC LIMIT $%d",
			tzIdx, whereSQL, tsCursorIdx, tsCursorIdx, idCursorIdx, limitIdx,
		)

		fullArgs := append(args, tz, ts, ts, id, limit)
		rows, err := database.Query(query, fullArgs...)
		if err != nil {
			slog.Error("export API: cursor query failed", "error", err)
			return []gin.H{}, false, ""
		}
		defer rows.Close()

		logs = scanLogRowsJSONWithEpoch(rows)
		hasMore = len(logs) == limit
	} else {
		tzIdx := len(args) + 1
		limitIdx := tzIdx + 1

		query := fmt.Sprintf(
			"SELECT to_char((timestamp AT TIME ZONE $%d) AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS') as ts, id, hostname, fromhost_ip, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, matched_parsers, extract(epoch from timestamp) as ts_epoch FROM syslog_logs %s ORDER BY timestamp DESC, id DESC LIMIT $%d",
			tzIdx, whereSQL, limitIdx,
		)

		fullArgs := append(args, tz, limit)
		rows, err := database.Query(query, fullArgs...)
		if err != nil {
			slog.Error("export API: query failed", "error", err)
			return []gin.H{}, false, ""
		}
		defer rows.Close()

		logs = scanLogRowsJSONWithEpoch(rows)
		hasMore = len(logs) == limit
	}

	if hasMore && len(logs) > 0 {
		last := logs[len(logs)-1]
		if tsEpoch, ok := last["ts_epoch"].(float64); ok {
			if idVal, ok := last["id"].(float64); ok {
				ts := time.Unix(int64(tsEpoch), 0).UTC()
				nextCursor = encodeLogCursor(ts, int64(idVal))
			}
		}
	}

	for i := range logs {
		delete(logs[i], "id")
		delete(logs[i], "ts_epoch")
	}

	return logs, hasMore, nextCursor
}

func scanLogRowsJSON(rows *sql.Rows) []gin.H {
	var logs []gin.H
	for rows.Next() {
		var tsStr string
		var id float64
		var hostname, severity, facility, message string
		var fromHostIP, appName, processID, msgID, rawMessage sql.NullString
		var parsedFields json.RawMessage
		var matchedParsers pq.StringArray

		err := rows.Scan(&tsStr, &id, &hostname, &fromHostIP, &appName, &processID, &msgID, &severity, &facility, &message, &rawMessage, &parsedFields, &matchedParsers)
		if err != nil {
			slog.Error("export API: row scan failed", "error", err)
			continue
		}

		l := gin.H{}
		l["hostname"] = hostname
		l["severity"] = severity
		l["facility"] = facility
		l["message"] = message
		l["ts"] = tsStr
		l["id"] = id
		l["fromhost_ip"] = fromHostIP.String
		l["app_name"] = appName.String
		l["process_id"] = processID.String
		l["msg_id"] = msgID.String
		l["raw_message"] = rawMessage.String

		var pf map[string]string
		if len(parsedFields) > 0 {
			json.Unmarshal(parsedFields, &pf)
		}
		l["parsed_fields"] = pf
		l["matched_parsers"] = matchedParsers

		logs = append(logs, l)
	}
	if logs == nil {
		logs = []gin.H{}
	}
	return logs
}

// scanLogRowsJSONWithEpoch is like scanLogRowsJSON but also scans the
// ts_epoch column for accurate cursor encoding regardless of timezone.
func scanLogRowsJSONWithEpoch(rows *sql.Rows) []gin.H {
	var logs []gin.H
	for rows.Next() {
		var tsStr string
		var id, tsEpoch float64
		var hostname, severity, facility, message string
		var fromHostIP, appName, processID, msgID, rawMessage sql.NullString
		var parsedFields json.RawMessage
		var matchedParsers pq.StringArray

		err := rows.Scan(&tsStr, &id, &hostname, &fromHostIP, &appName, &processID, &msgID, &severity, &facility, &message, &rawMessage, &parsedFields, &matchedParsers, &tsEpoch)
		if err != nil {
			slog.Error("export API: row scan failed", "error", err)
			continue
		}

		l := gin.H{}
		l["hostname"] = hostname
		l["severity"] = severity
		l["facility"] = facility
		l["message"] = message
		l["ts"] = tsStr
		l["id"] = id
		l["ts_epoch"] = tsEpoch
		l["fromhost_ip"] = fromHostIP.String
		l["app_name"] = appName.String
		l["process_id"] = processID.String
		l["msg_id"] = msgID.String
		l["raw_message"] = rawMessage.String

		var pf map[string]string
		if len(parsedFields) > 0 {
			json.Unmarshal(parsedFields, &pf)
		}
		l["parsed_fields"] = pf
		l["matched_parsers"] = matchedParsers

		logs = append(logs, l)
	}
	if logs == nil {
		logs = []gin.H{}
	}
	return logs
}
