package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"syslog-gui/middleware"
	"syslog-gui/model"

	"github.com/gin-gonic/gin"
)

var (
	dashboardCache     model.DashboardStats
	dashboardCacheMu   sync.RWMutex
	dashboardCacheTime time.Time
	dashboardTTL       = 30 * time.Second

	deviceStatsCache     []model.DeviceStats
	deviceStatsCacheMu   sync.RWMutex
	deviceStatsCacheTime time.Time
	deviceStatsTTL       = 60 * time.Second

	severityStatsCache     []model.SeverityStats
	severityStatsCacheMu   sync.RWMutex
	severityStatsCacheTime time.Time
	severityStatsTTL       = 30 * time.Second
)

func statsInvalidateAll() {
	dashboardCacheMu.Lock()
	dashboardCache = model.DashboardStats{}
	dashboardCacheTime = time.Time{}
	dashboardCacheMu.Unlock()

	deviceStatsCacheMu.Lock()
	deviceStatsCache = nil
	deviceStatsCacheTime = time.Time{}
	deviceStatsCacheMu.Unlock()

	severityStatsCacheMu.Lock()
	severityStatsCache = nil
	severityStatsCacheTime = time.Time{}
	severityStatsCacheMu.Unlock()
}

func GetDashboardStats(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		from := c.Query("from")
		to := c.Query("to")

		dashboardCacheMu.RLock()
		if time.Since(dashboardCacheTime) < dashboardTTL && from == "" && to == "" {
			stats := dashboardCache
			dashboardCacheMu.RUnlock()
			c.JSON(http.StatusOK, stats)
			return
		}
		dashboardCacheMu.RUnlock()

		stats := buildDashboardStats(db, from, to)

		if from == "" && to == "" {
			dashboardCacheMu.Lock()
			dashboardCache = stats
			dashboardCacheTime = time.Now()
			dashboardCacheMu.Unlock()
		}

		c.JSON(http.StatusOK, stats)
	}
}

func buildDashboardStats(db *sql.DB, from, to string) model.DashboardStats {
	var stats model.DashboardStats

	whereBase := ""
	args := []interface{}{}
	argIdx := 1

	if from != "" {
		whereBase = fmt.Sprintf("WHERE timestamp >= $%d", argIdx)
		args = append(args, from)
		argIdx++
	} else {
		whereBase = "WHERE timestamp >= NOW() - INTERVAL '7 days'"
	}

	if to != "" {
		whereBase += fmt.Sprintf(" AND timestamp <= $%d", argIdx)
		args = append(args, to)
		argIdx++
	}

	// Single query for scalar metrics (total, last hour, last day, unique devices)
	row := db.QueryRow(fmt.Sprintf(
		"SELECT COUNT(*), "+
			"COUNT(*) FILTER (WHERE timestamp >= NOW() - INTERVAL '1 hour'), "+
			"COUNT(*) FILTER (WHERE timestamp >= NOW() - INTERVAL '1 day'), "+
			"COUNT(DISTINCT hostname) "+
			"FROM syslog_logs %s", whereBase), args...)
	row.Scan(&stats.TotalLogs, &stats.LogsLastHour, &stats.LogsLastDay, &stats.UniqueDevices)

	stats.SeverityCounts = make(map[string]int64)
	sevRows, err := db.Query(fmt.Sprintf("SELECT severity, COUNT(*) FROM syslog_logs %s GROUP BY severity ORDER BY COUNT(*) DESC", whereBase), args...)
	if err == nil {
		defer sevRows.Close()
		for sevRows.Next() {
			var sev string
			var cnt int64
			if sevRows.Scan(&sev, &cnt) == nil {
				stats.SeverityCounts[sev] = cnt
			}
		}
	}

	devRows, err := db.Query(fmt.Sprintf(
		"SELECT COALESCE(MIN(fromhost_ip), ''), MIN(hostname) as hostname, COUNT(*) as cnt FROM syslog_logs %s GROUP BY fromhost_ip ORDER BY cnt DESC LIMIT 10", whereBase), args...)
	if err == nil {
		defer devRows.Close()
		for devRows.Next() {
			var d model.DeviceCount
			if devRows.Scan(&d.FromHostIP, &d.Hostname, &d.Count) == nil {
				stats.TopDevices = append(stats.TopDevices, d)
			}
		}
	}

	if stats.TopDevices == nil {
		stats.TopDevices = []model.DeviceCount{}
	}

	errRows, err := db.Query(fmt.Sprintf(
		"SELECT substring(message from 1 for 100) as msg, COALESCE(MIN(fromhost_ip), ''), MIN(hostname) as hostname, COUNT(*) as cnt "+
			"FROM syslog_logs %s AND severity IN ('err', 'crit', 'alert', 'emerg') "+
			"GROUP BY msg, fromhost_ip ORDER BY cnt DESC LIMIT 10", whereBase), args...)
	if err == nil {
		defer errRows.Close()
		for errRows.Next() {
			var e model.ErrorMessage
			if errRows.Scan(&e.Message, &e.FromHostIP, &e.Hostname, &e.Count) == nil {
				stats.TopErrors = append(stats.TopErrors, e)
			}
		}
	}

	if stats.TopErrors == nil {
		stats.TopErrors = []model.ErrorMessage{}
	}

	return stats
}

func GetDeviceStats(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		limitStr := c.DefaultQuery("limit", "100")
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit <= 0 {
			limit = 100
		}
		if limit > 1000 {
			limit = 1000
		}

		if limit == 100 {
			deviceStatsCacheMu.RLock()
			if time.Since(deviceStatsCacheTime) < deviceStatsTTL && deviceStatsCache != nil {
				devices := deviceStatsCache
				deviceStatsCacheMu.RUnlock()
				c.JSON(http.StatusOK, gin.H{"devices": devices})
				return
			}
			deviceStatsCacheMu.RUnlock()
		}

		devices := fetchDeviceStats(db, limit)

		if limit == 100 {
			deviceStatsCacheMu.Lock()
			deviceStatsCache = devices
			deviceStatsCacheTime = time.Now()
			deviceStatsCacheMu.Unlock()
		}

		c.JSON(http.StatusOK, gin.H{"devices": devices})
	}
}

func fetchDeviceStats(db *sql.DB, limit int) []model.DeviceStats {
	rows, err := db.Query(
		fmt.Sprintf(`SELECT COALESCE(MIN(fromhost_ip), ''), MIN(hostname) as hostname, COUNT(*) as total, MAX(timestamp) as last_seen,
			SUM(CASE WHEN severity = 'emergency' THEN 1 ELSE 0 END),
			SUM(CASE WHEN severity = 'alert' THEN 1 ELSE 0 END),
			SUM(CASE WHEN severity = 'critical' THEN 1 ELSE 0 END),
			SUM(CASE WHEN severity = 'error' THEN 1 ELSE 0 END),
			SUM(CASE WHEN severity = 'warning' THEN 1 ELSE 0 END),
			SUM(CASE WHEN severity = 'notice' THEN 1 ELSE 0 END),
			SUM(CASE WHEN severity = 'info' THEN 1 ELSE 0 END),
			SUM(CASE WHEN severity = 'debug' THEN 1 ELSE 0 END)
			FROM syslog_logs GROUP BY fromhost_ip ORDER BY total DESC LIMIT %d`, limit),
	)
	if err != nil {
		return []model.DeviceStats{}
	}
	defer rows.Close()

	var devices []model.DeviceStats
	for rows.Next() {
		var d model.DeviceStats
		var emergency, alert, critical, errCount, warning, notice, info, debug int64
		if err := rows.Scan(&d.FromHostIP, &d.Hostname, &d.TotalLogs, &d.LastSeen,
			&emergency, &alert, &critical, &errCount, &warning, &notice, &info, &debug); err != nil {
			continue
		}
		d.SeverityCount = model.SeverityCounts{
			"emergency": emergency, "alert": alert, "critical": critical, "error": errCount,
			"warning": warning, "notice": notice, "info": info, "debug": debug,
		}
		devices = append(devices, d)
	}

	if devices == nil {
		devices = []model.DeviceStats{}
	}
	return devices
}

func GetSeverityStats(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		from := c.Query("from")
		to := c.Query("to")

		if from == "" && to == "" {
			severityStatsCacheMu.RLock()
			if time.Since(severityStatsCacheTime) < severityStatsTTL && severityStatsCache != nil {
				stats := severityStatsCache
				severityStatsCacheMu.RUnlock()
				c.JSON(http.StatusOK, gin.H{"stats": stats})
				return
			}
			severityStatsCacheMu.RUnlock()
		}

		var stats []model.SeverityStats
		if from == "" && to == "" {
			stats = fetchSeverityStatsAll(db)
		} else {
			stats = fetchSeverityStatsRange(db, from, to)
		}

		if from == "" && to == "" {
			severityStatsCacheMu.Lock()
			severityStatsCache = stats
			severityStatsCacheTime = time.Now()
			severityStatsCacheMu.Unlock()
		}

		c.JSON(http.StatusOK, gin.H{"stats": stats})
	}
}

func fetchSeverityStatsAll(db *sql.DB) []model.SeverityStats {
	rows, err := db.Query(`
		SELECT severity, COUNT(*) FROM syslog_logs
		GROUP BY severity ORDER BY COUNT(*) DESC
	`)
	if err != nil {
		return []model.SeverityStats{}
	}
	defer rows.Close()

	var stats []model.SeverityStats
	for rows.Next() {
		var s model.SeverityStats
		if rows.Scan(&s.Severity, &s.Count) == nil {
			stats = append(stats, s)
		}
	}

	if stats == nil {
		stats = []model.SeverityStats{}
	}
	return stats
}

func fetchSeverityStatsRange(db *sql.DB, from, to string) []model.SeverityStats {
	where := ""
	args := []interface{}{}
	argIdx := 1

	if from != "" {
		where += fmt.Sprintf(" WHERE timestamp >= $%d", argIdx)
		args = append(args, from)
		argIdx++
	}
	if to != "" {
		if where == "" {
			where += " WHERE"
		} else {
			where += " AND"
		}
		where += fmt.Sprintf(" timestamp <= $%d", argIdx)
		args = append(args, to)
	}

	rows, err := db.Query(
		"SELECT severity, COUNT(*) FROM syslog_logs"+where+" GROUP BY severity ORDER BY COUNT(*) DESC",
		args...,
	)
	if err != nil {
		return []model.SeverityStats{}
	}
	defer rows.Close()

	var stats []model.SeverityStats
	for rows.Next() {
		var s model.SeverityStats
		if rows.Scan(&s.Severity, &s.Count) == nil {
			stats = append(stats, s)
		}
	}

	if stats == nil {
		stats = []model.SeverityStats{}
	}
	return stats
}

func GetTimelineStats(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		interval := c.DefaultQuery("interval", "hour")
		from := c.Query("from")
		to := c.Query("to")

		fieldMap := map[string]string{"1m": "minute", "5m": "minute", "15m": "minute", "30m": "minute", "1h": "hour", "6h": "hour", "1d": "day", "1w": "week", "1mon": "month", "minute": "minute", "hour": "hour", "day": "day", "week": "week", "month": "month"}
		field, ok := fieldMap[interval]
		if !ok {
			field = "hour"
		}

		var query string
		args := []interface{}{field}
		argIdx := 2

		if from == "" {
			query = "SELECT date_trunc($1, timestamp) as ts, COUNT(*) FROM syslog_logs WHERE timestamp >= now() - interval '24 hours'"
		} else {
			query = fmt.Sprintf("SELECT date_trunc($1, timestamp) as ts, COUNT(*) FROM syslog_logs WHERE timestamp >= $%d", argIdx)
			args = append(args, from)
			argIdx++
		}

		if to != "" {
			query += fmt.Sprintf(" AND timestamp <= $%d", argIdx)
			args = append(args, to)
		}

		query += " GROUP BY ts ORDER BY ts"

		rows, err := db.Query(query, args...)
		if err != nil {
			middleware.HandleError(c, model.NewInternal("Query failed", err))
			return
		}
		defer rows.Close()

		var points []model.TimelinePoint
		for rows.Next() {
			var p model.TimelinePoint
			if rows.Scan(&p.Timestamp, &p.Count) == nil {
				points = append(points, p)
			}
		}

		c.JSON(http.StatusOK, gin.H{"timeline": points})
	}
}
