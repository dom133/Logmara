package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"syslog-gui/control"
	"syslog-gui/model"
	"syslog-gui/parser"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

func IngestBatch(db *sql.DB, engine *parser.Engine, ic *control.IngestionController) gin.HandlerFunc {
	return func(c *gin.Context) {
		if ic.IsPaused() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Ingestion is paused"})
			return
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Could not read body"})
			return
		}

		var entries []model.IngestEntry
		if err := json.Unmarshal(body, &entries); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON: " + err.Error()})
			return
		}

		if len(entries) == 0 {
			c.JSON(http.StatusOK, gin.H{"ingested": 0})
			return
		}

		tx, err := db.Begin()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not begin transaction"})
			return
		}
		defer tx.Rollback()

query := `INSERT INTO syslog_logs (timestamp, hostname, fromhost_ip, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, matched_parsers)
		          VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
		stmt, err := tx.Prepare(query)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not prepare statement"})
			return
		}
		defer stmt.Close()

		ingested := 0
		for _, entry := range entries {
			ts, err := parseTimestamp(entry.Timestamp)
			if err != nil {
				log.Printf("Invalid timestamp: %s, using now", entry.Timestamp)
				ts = time.Now()
			}

			fromHostIP := nullStr(entry.FromHostIP)
			appName := nullStr(entry.AppName)
			processID := nullStr(entry.ProcessID)
			msgID := nullStr(entry.MsgID)
			facility := nullStr(entry.Facility)
			rawMsg := nullStr(entry.RawMessage)

			result := engine.Parse(entry.Hostname, entry.AppName, entry.Message)
			if result == nil {
				parsedJSON := []byte("null")
				_, err = stmt.Exec(ts, entry.Hostname, fromHostIP, appName, processID, msgID,
					entry.Severity, facility, entry.Message, rawMsg, parsedJSON, pq.StringArray(nil))
				if err != nil {
					log.Printf("Insert error: %v", err)
				}
				ingested++
				continue
			}
			parsedJSON, marshalErr := json.Marshal(result.Fields)
			if marshalErr != nil {
				parsedJSON = []byte("null")
			}

			_, err = stmt.Exec(ts, entry.Hostname, fromHostIP, appName, processID, msgID,
				entry.Severity, facility, entry.Message, rawMsg, parsedJSON, pq.StringArray(result.Parsers))
			if err != nil {
				log.Printf("Insert error: %v", err)
				continue
			}
			ingested++
		}

		if err := tx.Commit(); err != nil {
			log.Printf("Commit error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction commit failed"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"ingested": ingested})
	}
}

func GetLogs(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := c.DefaultQuery("limit", "50")
		offset := c.DefaultQuery("offset", "0")
		hostname := c.Query("hostname")
		fromHostIP := c.Query("fromhost_ip")
		severity := c.Query("severity")
		appName := c.Query("app_name")
		search := c.Query("search")
		from := c.Query("from")
		to := c.Query("to")
		sort := c.DefaultQuery("sort", "timestamp_desc")

		limitInt, _ := strconv.Atoi(limit)
		offsetInt, _ := strconv.Atoi(offset)

		if limitInt > 1000 {
			limitInt = 1000
		}

		whereClauses := []string{}
		args := []interface{}{}
		argIdx := 1

		if hostname != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("hostname = $%d", argIdx))
			args = append(args, hostname)
			argIdx++
		}

		if fromHostIP != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("COALESCE(fromhost_ip, '') = $%d", argIdx))
			args = append(args, fromHostIP)
			argIdx++
		}

		if severity != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("severity = $%d", argIdx))
			args = append(args, severity)
			argIdx++
		}

		if appName != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("app_name ILIKE $%d", argIdx))
			args = append(args, "%"+appName+"%")
			argIdx++
		}

		if search != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("(message ILIKE $%d OR raw_message ILIKE $%d)", argIdx, argIdx))
			args = append(args, "%"+search+"%")
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

		whereSQL := ""
		if len(whereClauses) > 0 {
			whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
		}

		orderClause := "timestamp DESC"
		switch sort {
		case "timestamp_asc":
			orderClause = "timestamp ASC"
		case "severity":
			orderClause = "severity ASC, timestamp DESC"
		case "hostname":
			orderClause = "hostname ASC, timestamp DESC"
		}

		// Count total
		countQuery := fmt.Sprintf("SELECT COUNT(*) FROM syslog_logs %s", whereSQL)
		var total int64
		if err := db.QueryRow(countQuery, args...).Scan(&total); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Count query failed"})
			return
		}

		// Fetch logs
		logsQuery := fmt.Sprintf(
			"SELECT id, timestamp, hostname, fromhost_ip, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, matched_parsers, created_at "+
				"FROM syslog_logs %s ORDER BY %s LIMIT $%d OFFSET $%d",
			whereSQL, orderClause, argIdx, argIdx+1,
		)
		args = append(args, limitInt, offsetInt)

		rows, err := db.Query(logsQuery, args...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Query failed: " + err.Error()})
			return
		}
		defer rows.Close()

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
				log.Printf("Scan error: %v", err)
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

		c.JSON(http.StatusOK, gin.H{
			"logs":   logs,
			"total":  total,
			"limit":  limitInt,
			"offset": offsetInt,
		})
	}
}

func GetDevices(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := db.Query(`SELECT COALESCE(MIN(fromhost_ip), ''), MIN(hostname) as hostname, COUNT(*) as total_logs, MAX(timestamp) as last_seen,
			SUM(CASE WHEN severity = 'emergency' THEN 1 ELSE 0 END) as emergency,
			SUM(CASE WHEN severity = 'alert' THEN 1 ELSE 0 END) as alert,
			SUM(CASE WHEN severity = 'critical' THEN 1 ELSE 0 END) as critical,
			SUM(CASE WHEN severity = 'error' THEN 1 ELSE 0 END) as error,
			SUM(CASE WHEN severity = 'warning' THEN 1 ELSE 0 END) as warning,
			SUM(CASE WHEN severity = 'notice' THEN 1 ELSE 0 END) as notice,
			SUM(CASE WHEN severity = 'info' THEN 1 ELSE 0 END) as info,
			SUM(CASE WHEN severity = 'debug' THEN 1 ELSE 0 END) as debug
			FROM syslog_logs GROUP BY fromhost_ip ORDER BY total_logs DESC`)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Query failed"})
			return
		}
		defer rows.Close()

		var devices []model.DeviceStats
		for rows.Next() {
			var ds model.DeviceStats
			var total int64
			var emergency, alert, critical, errCount, warning, notice, info, debug int64
			if err := rows.Scan(&ds.FromHostIP, &ds.Hostname, &total, &ds.LastSeen, &emergency, &alert, &critical, &errCount, &warning, &notice, &info, &debug); err != nil {
				continue
			}
			ds.TotalLogs = total
			ds.SeverityCount = model.SeverityCounts{"emergency": emergency, "alert": alert, "critical": critical, "error": errCount, "warning": warning, "notice": notice, "info": info, "debug": debug}

			pRows, _ := db.Query("SELECT ARRAY(SELECT DISTINCT unnest(matched_parsers) FROM syslog_logs WHERE COALESCE(fromhost_ip, '') = $1 AND matched_parsers IS NOT NULL AND matched_parsers != '{}')", ds.FromHostIP)
			if pRows != nil {
				defer pRows.Close()
				if pRows.Next() {
					var parsersArr pq.StringArray
					if err := pRows.Scan(&parsersArr); err == nil {
						ds.MatchedParsers = parsersArr
					}
				}
				ds.HasParsed = len(ds.MatchedParsers) > 0
			}

			var alias sql.NullString
			db.QueryRow("SELECT display_name FROM device_aliases WHERE fromhost_ip = $1", ds.FromHostIP).Scan(&alias)
			if alias.Valid {
				ds.DisplayName = alias.String
			}

			devices = append(devices, ds)
		}

		c.JSON(http.StatusOK, gin.H{"devices": devices})
	}
}

func UpdateDeviceAlias(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.Param("ip")
		var body struct {
			DisplayName string `json:"display_name" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
			return
		}
		_, err := db.Exec(
			`INSERT INTO device_aliases (fromhost_ip, display_name) VALUES ($1, $2)
			 ON CONFLICT (fromhost_ip) DO UPDATE SET display_name = $2, updated_at = NOW()`,
			ip, body.DisplayName,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update alias"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

func parseTimestamp(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"Jan 2 15:04:05",
		time.UnixDate,
	}

	for _, format := range formats {
		t, err := time.Parse(format, s)
		if err == nil {
			return t, nil
		}
	}

	// Try unix timestamp
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Unix(int64(f), 0), nil
	}

	return time.Now(), fmt.Errorf("unparseable timestamp: %s", s)
}

func matchGlob(pattern, value string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	matched, _ := filepath.Match(pattern, value)
	return matched
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
