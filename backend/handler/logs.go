package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"syslog-gui/control"
	"syslog-gui/middleware"
	"syslog-gui/model"
	"syslog-gui/parser"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

var (
	devicesCache     []model.DeviceStats
	devicesCacheMu   sync.RWMutex
	devicesCacheTime time.Time
	devicesTTL       = 60 * time.Second
)

func InvalidateAllCaches() {
	devicesCacheMu.Lock()
	devicesCache = nil
	devicesCacheTime = time.Time{}
	devicesCacheMu.Unlock()

	statsInvalidateAll()
}

func IngestBatch(db *sql.DB, engine *parser.Engine, ic *control.IngestionController) gin.HandlerFunc {
	return func(c *gin.Context) {
		if ic.IsPaused() {
			middleware.HandleError(c, model.NewServiceUnavailable("Ingestion is paused", nil))
			return
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("Could not read body", err))
			return
		}

		var entries []model.IngestEntry
		if err := json.Unmarshal(body, &entries); err != nil {
			middleware.HandleError(c, model.NewBadRequest("Invalid JSON", err))
			return
		}

		if len(entries) == 0 {
			c.JSON(http.StatusOK, gin.H{"ingested": 0})
			return
		}

		tx, err := db.Begin()
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Could not begin transaction", err))
			return
		}
		defer tx.Rollback()

		query := `INSERT INTO syslog_logs (timestamp, hostname, fromhost_ip, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, matched_parsers)
		          VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
		stmt, err := tx.Prepare(query)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Could not prepare statement", err))
			return
		}
		defer stmt.Close()

		ingested := 0
		for _, entry := range entries {
			ts, err := parseTimestamp(entry.Timestamp)
			if err != nil {
				slog.Warn("invalid timestamp, using now", "value", entry.Timestamp)
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
					slog.Error("insert error", "error", err)
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
				slog.Error("insert error", "error", err)
				continue
			}
			ingested++
		}

		if err := tx.Commit(); err != nil {
			slog.Error("commit error", "error", err)
			middleware.HandleError(c, model.NewInternal("Transaction commit failed", err))
			return
		}

		InvalidateAllCaches()
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

		if limitInt > MaxLogLimit {
			limitInt = MaxLogLimit
		}

		opts := LogFilterOptions{
			Hostname:   hostname,
			FromHostIP: fromHostIP,
			Severity:   severity,
			AppName:    appName,
			Search:     search,
			From:       from,
			To:         to,
		}
		whereClauses, args, argIdx := buildLogWhereClauses(opts)
		whereSQL := buildWhereSQL(whereClauses)

		orderClause := "timestamp DESC"
		switch sort {
		case "timestamp_asc":
			orderClause = "timestamp ASC"
		case "severity":
			orderClause = "severity ASC, timestamp DESC"
		case "hostname":
			orderClause = "hostname ASC, timestamp DESC"
		}

		countQuery := fmt.Sprintf("SELECT COUNT(*) FROM syslog_logs %s", whereSQL)
		var total int64
		if err := db.QueryRow(countQuery, args...).Scan(&total); err != nil {
			middleware.HandleError(c, model.NewInternal("Count query failed", err))
			return
		}

		logsQuery := fmt.Sprintf(
			"SELECT id, timestamp, hostname, syslog_logs.fromhost_ip, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, matched_parsers, created_at, COALESCE(da.display_name, '') "+
				"FROM syslog_logs %s LEFT JOIN device_aliases da ON da.fromhost_ip = COALESCE(syslog_logs.fromhost_ip, '') ORDER BY %s LIMIT $%d OFFSET $%d",
			whereSQL, orderClause, argIdx, argIdx+1,
		)
		args = append(args, limitInt, offsetInt)

		rows, err := db.Query(logsQuery, args...)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Query failed", err))
			return
		}
		defer rows.Close()

		logs := scanLogRows(rows)

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
		devicesCacheMu.RLock()
		if time.Since(devicesCacheTime) < devicesTTL && devicesCache != nil {
			devices := devicesCache
			devicesCacheMu.RUnlock()
			c.JSON(http.StatusOK, gin.H{"devices": devices})
			return
		}
		devicesCacheMu.RUnlock()

		devices := fetchDevices(db)

		devicesCacheMu.Lock()
		devicesCache = devices
		devicesCacheTime = time.Now()
		devicesCacheMu.Unlock()

		c.JSON(http.StatusOK, gin.H{"devices": devices})
	}
}

func fetchDevices(db *sql.DB) []model.DeviceStats {
	rows, err := db.Query(`
		WITH dev_stats AS (
			SELECT COALESCE(MIN(fromhost_ip), '') as fromhost_ip, MIN(hostname) as hostname,
				COUNT(*) as total_logs, MAX(timestamp) as last_seen,
				SUM(CASE WHEN severity = 'emergency' THEN 1 ELSE 0 END) as emergency,
				SUM(CASE WHEN severity = 'alert' THEN 1 ELSE 0 END) as alert,
				SUM(CASE WHEN severity = 'critical' THEN 1 ELSE 0 END) as critical,
				SUM(CASE WHEN severity = 'error' THEN 1 ELSE 0 END) as err_count,
				SUM(CASE WHEN severity = 'warning' THEN 1 ELSE 0 END) as warning,
				SUM(CASE WHEN severity = 'notice' THEN 1 ELSE 0 END) as notice,
				SUM(CASE WHEN severity = 'info' THEN 1 ELSE 0 END) as info,
				SUM(CASE WHEN severity = 'debug' THEN 1 ELSE 0 END) as debug
			FROM syslog_logs
			GROUP BY fromhost_ip
		),
		dev_parsers AS (
			SELECT COALESCE(fromhost_ip, '') as fromhost_ip,
				array_agg(DISTINCT elem) as parsers
			FROM syslog_logs, unnest(matched_parsers) as elem
			WHERE matched_parsers IS NOT NULL AND matched_parsers != '{}'
			GROUP BY fromhost_ip
		)
		SELECT d.fromhost_ip, d.hostname, d.total_logs, d.last_seen,
			d.emergency, d.alert, d.critical, d.err_count, d.warning, d.notice, d.info, d.debug,
			COALESCE(p.parsers, '{}'::TEXT[]) as parsers,
			a.display_name, a.old_hostname
		FROM dev_stats d
		LEFT JOIN dev_parsers p ON p.fromhost_ip = d.fromhost_ip
		LEFT JOIN device_aliases a ON a.fromhost_ip = d.fromhost_ip
		ORDER BY d.total_logs DESC
	`)
	if err != nil {
		return []model.DeviceStats{}
	}
	defer rows.Close()

	var devices []model.DeviceStats
	for rows.Next() {
		var ds model.DeviceStats
		var total int64
		var emergency, alert, critical, errCount, warning, notice, info, debug int64
		var parsersArr pq.StringArray
		var alias, oldH sql.NullString
		if err := rows.Scan(&ds.FromHostIP, &ds.Hostname, &total, &ds.LastSeen,
			&emergency, &alert, &critical, &errCount, &warning, &notice, &info, &debug,
			&parsersArr, &alias, &oldH); err != nil {
			continue
		}
		ds.TotalLogs = total
		ds.SeverityCount = model.SeverityCounts{"emergency": emergency, "alert": alert, "critical": critical, "error": errCount, "warning": warning, "notice": notice, "info": info, "debug": debug}
		ds.MatchedParsers = parsersArr
		ds.HasParsed = len(parsersArr) > 0
		if alias.Valid {
			ds.DisplayName = alias.String
		}
		if oldH.Valid {
			ds.OldHostname = oldH.String
		}
		devices = append(devices, ds)
	}

	if devices == nil {
		devices = []model.DeviceStats{}
	}
	return devices
}

func UpdateDeviceAlias(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.Param("ip")
		var body struct {
			DisplayName string `json:"display_name" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			middleware.HandleError(c, model.NewBadRequest("Invalid request", err))
			return
		}
		var curHostname sql.NullString
		db.QueryRow("SELECT hostname FROM syslog_logs WHERE COALESCE(fromhost_ip, '') = $1 ORDER BY timestamp DESC LIMIT 1", ip).Scan(&curHostname)
		oldhn := curHostname.String
		_, err := db.Exec(
			`INSERT INTO device_aliases (fromhost_ip, display_name, old_hostname) VALUES ($1, $2, $3)
			 ON CONFLICT (fromhost_ip) DO UPDATE SET display_name = $2, old_hostname = $3, updated_at = NOW()`,
			ip, body.DisplayName, oldhn,
		)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Failed to update alias", err))
			return
		}
		InvalidateAllCaches()
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
