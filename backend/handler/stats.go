package handler

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"syslog-gui/model"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

var (
	dashboardCache     model.DashboardStats
	dashboardCacheMu   sync.RWMutex
	dashboardCacheTime time.Time
	dashboardTTL       = 1 * time.Minute

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

// filteredAggregates holds every "top N" / grouped stat computed over the
// same filtered row set (scalar counts, severity breakdown, top devices,
// top errors). It is populated by a single combined query so the filtered
// range is scanned once instead of once per aggregate.
type filteredAggregates struct {
	TotalLogs     int64
	LogsLastHour  int64
	LogsLastDay   int64
	UniqueDevices int64
	Severity      map[string]int64
	TopDevices    []model.DeviceCount
	TopErrors     []model.ErrorMessage
}

// queryFilteredAggregates computes scalar counts, the severity breakdown,
// and the top-10 devices/errors for whereBase in one query. Only used for a
// custom (from/to) date range - the unfiltered dashboard load is served
// entirely from materialized views kept fresh by the background scheduler
// (see buildDashboardStats). The "filtered" CTE is marked MATERIALIZED so
// Postgres scans syslog_logs exactly once and reuses that result for every
// branch below, instead of re-running whereBase once per aggregate.
func queryFilteredAggregates(db *sql.DB, whereBase string, args []interface{}) filteredAggregates {
	result := filteredAggregates{Severity: make(map[string]int64)}

	branches := []string{
		"SELECT 'scalar'::text AS kind, 'total_logs'::text AS key1, NULL::text AS key2, NULL::text AS key3, COUNT(*)::bigint AS cnt FROM filtered",
		"SELECT 'scalar'::text, 'logs_last_hour'::text, NULL::text, NULL::text, COUNT(*) FILTER (WHERE timestamp >= NOW() - INTERVAL '1 hour')::bigint FROM filtered",
		"SELECT 'scalar'::text, 'logs_last_day'::text, NULL::text, NULL::text, COUNT(*) FILTER (WHERE timestamp >= NOW() - INTERVAL '1 day')::bigint FROM filtered",
		"SELECT 'scalar'::text, 'unique_devices'::text, NULL::text, NULL::text, COUNT(DISTINCT fromhost_ip)::bigint FROM filtered",
		"SELECT 'severity'::text, severity::text, NULL::text, NULL::text, COUNT(*)::bigint FROM filtered GROUP BY severity",
		"SELECT * FROM (SELECT 'device'::text AS kind, COALESCE(fromhost_ip,'')::text AS key1, MIN(hostname)::text AS key2, NULL::text AS key3, COUNT(*)::bigint AS cnt FROM filtered GROUP BY fromhost_ip ORDER BY cnt DESC LIMIT 10) d",
		"SELECT * FROM (SELECT 'error'::text AS kind, LEFT(message, 100)::text AS key1, COALESCE(fromhost_ip,'')::text AS key2, MIN(hostname)::text AS key3, COUNT(*)::bigint AS cnt FROM filtered WHERE severity IN ('err', 'crit', 'alert', 'emerg') GROUP BY LEFT(message, 100), fromhost_ip ORDER BY cnt DESC LIMIT 10) e",
	}

	query := fmt.Sprintf(
		"WITH filtered AS MATERIALIZED (SELECT timestamp, hostname, fromhost_ip, severity, message FROM syslog_logs %s) %s",
		whereBase, strings.Join(branches, " UNION ALL "),
	)

	rows, err := db.Query(query, args...)
	if err != nil {
		slog.Error("filtered aggregates query failed", "err", err)
		return result
	}
	defer rows.Close()

	for rows.Next() {
		var kind string
		var key1, key2, key3 sql.NullString
		var cnt int64
		if rows.Scan(&kind, &key1, &key2, &key3, &cnt) != nil {
			continue
		}
		switch kind {
		case "scalar":
			switch key1.String {
			case "total_logs":
				result.TotalLogs = cnt
			case "logs_last_hour":
				result.LogsLastHour = cnt
			case "logs_last_day":
				result.LogsLastDay = cnt
			case "unique_devices":
				result.UniqueDevices = cnt
			}
		case "severity":
			result.Severity[key1.String] = cnt
		case "device":
			result.TopDevices = append(result.TopDevices, model.DeviceCount{FromHostIP: key1.String, Hostname: key2.String, Count: cnt})
		case "error":
			result.TopErrors = append(result.TopErrors, model.ErrorMessage{Message: key1.String, FromHostIP: key2.String, Hostname: key3.String, Count: cnt})
		}
	}
	return result
}

func buildDashboardStats(db *sql.DB, from, to string) model.DashboardStats {
	var stats model.DashboardStats
	stats.SeverityCounts = make(map[string]int64)

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

	useMV := from == "" && to == ""

	// Unfiltered dashboard load: served entirely from materialized views, no
	// live query against syslog_logs at all. All four MVs are kept fresh by
	// db.StartMaintenance's periodic scheduler (only while someone is logged
	// in), so the request path never blocks on a REFRESH or a multi-second
	// aggregate scan.
	if useMV {
		err := timedQuery("dashboard_stats_scalar_mv", func() error {
			var refreshedAt pq.NullTime
			// unique_ips (COUNT DISTINCT fromhost_ip) is used as the device count
			// so it agrees with mv_device_stats (grouped by fromhost_ip) - the
			// Admin Devices tab and Top Devices both key on fromhost_ip, not
			// hostname, which isn't guaranteed unique per device.
			row := db.QueryRow("SELECT total_logs, logs_last_hour, logs_last_day, unique_ips, refreshed_at FROM mv_dashboard_summary LIMIT 1")
			return row.Scan(&stats.TotalLogs, &stats.LogsLastHour, &stats.LogsLastDay, &stats.UniqueDevices, &refreshedAt)
		})
		if err != nil {
			_ = timedQuery("dashboard_stats_scalar_fallback", func() error {
				row := db.QueryRow("SELECT COUNT(*), COUNT(*) FILTER (WHERE timestamp >= NOW() - INTERVAL '1 hour'), COUNT(*) FILTER (WHERE timestamp >= NOW() - INTERVAL '1 day'), COUNT(DISTINCT fromhost_ip) FROM syslog_logs")
				return row.Scan(&stats.TotalLogs, &stats.LogsLastHour, &stats.LogsLastDay, &stats.UniqueDevices)
			})
		}
		_ = timedQuery("dashboard_stats_severity", func() error {
			sevRows, err := db.Query("SELECT severity, cnt FROM mv_dashboard_severity ORDER BY cnt DESC")
			if err != nil {
				return err
			}
			defer sevRows.Close()
			for sevRows.Next() {
				var sev string
				var cnt int64
				if sevRows.Scan(&sev, &cnt) == nil {
					stats.SeverityCounts[sev] = cnt
				}
			}
			return nil
		})
		stats.TopDevices = []model.DeviceCount{}
		_ = timedQuery("dashboard_stats_top_devices_mv", func() error {
			rows, err := db.Query(
				`SELECT fromhost_ip, hostname, total_logs FROM mv_device_stats ORDER BY total_logs DESC LIMIT 10`,
			)
			if err != nil {
				return err
			}
			defer rows.Close()
			topDevices := []model.DeviceCount{}
			for rows.Next() {
				var d model.DeviceCount
				if rows.Scan(&d.FromHostIP, &d.Hostname, &d.Count) == nil {
					topDevices = append(topDevices, d)
				}
			}
			stats.TopDevices = topDevices
			return nil
		})
		stats.TopErrors = []model.ErrorMessage{}
		_ = timedQuery("dashboard_stats_top_errors_mv", func() error {
			rows, err := db.Query(
				`SELECT message, fromhost_ip, hostname, cnt FROM mv_dashboard_top_errors ORDER BY cnt DESC LIMIT 10`,
			)
			if err != nil {
				return err
			}
			defer rows.Close()
			topErrors := []model.ErrorMessage{}
			for rows.Next() {
				var e model.ErrorMessage
				if rows.Scan(&e.Message, &e.FromHostIP, &e.Hostname, &e.Count) == nil {
					topErrors = append(topErrors, e)
				}
			}
			stats.TopErrors = topErrors
			return nil
		})
	} else {
		var agg filteredAggregates
		_ = timedQuery("dashboard_stats_filtered_aggregates", func() error {
			agg = queryFilteredAggregates(db, whereBase, args)
			return nil
		})
		stats.TotalLogs = agg.TotalLogs
		stats.LogsLastHour = agg.LogsLastHour
		stats.LogsLastDay = agg.LogsLastDay
		stats.UniqueDevices = agg.UniqueDevices
		stats.SeverityCounts = agg.Severity
		stats.TopDevices = agg.TopDevices
		stats.TopErrors = agg.TopErrors
	}

	if stats.TopDevices == nil {
		stats.TopDevices = []model.DeviceCount{}
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
	var devices []model.DeviceStats
	_ = timedQuery("device_stats_all", func() error {
		// Reads from the mv_device_stats rollup instead of aggregating
		// syslog_logs live - see fetchDevices in logs.go for the same fix.
		rows, err := db.Query(
			`SELECT fromhost_ip, hostname, total_logs, last_seen,
				emergency, alert, critical, err_count, warning, notice, info, debug
				FROM mv_device_stats ORDER BY total_logs DESC LIMIT $1`, limit,
		)
		if err != nil {
			return err
		}
		defer rows.Close()

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
		return nil
	})

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
	var stats []model.SeverityStats
	_ = timedQuery("severity_stats_all", func() error {
		rows, err := db.Query(`
			SELECT severity, COUNT(*) FROM syslog_logs
			GROUP BY severity ORDER BY COUNT(*) DESC
		`)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var s model.SeverityStats
			if rows.Scan(&s.Severity, &s.Count) == nil {
				stats = append(stats, s)
			}
		}
		return nil
	})

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

	var stats []model.SeverityStats
	_ = timedQuery("severity_stats_range", func() error {
		rows, err := db.Query(
			"SELECT severity, COUNT(*) FROM syslog_logs"+where+" GROUP BY severity ORDER BY COUNT(*) DESC",
			args...,
		)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var s model.SeverityStats
			if rows.Scan(&s.Severity, &s.Count) == nil {
				stats = append(stats, s)
			}
		}
		return nil
	})

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

		var points []model.TimelinePoint

		if from == "" && to == "" && field != "minute" {
			lookback := "24 hours"
			switch field {
			case "hour":
				lookback = "7 days"
			case "day":
				lookback = "30 days"
			case "week":
				lookback = "90 days"
			case "month":
				lookback = "365 days"
			}

			_ = timedQuery("timeline_stats", func() error {
				query := fmt.Sprintf(
					"SELECT date_trunc('%s', hour) as ts, SUM(cnt) as cnt FROM mv_timeline_hourly WHERE hour >= now() - interval '%s' GROUP BY ts ORDER BY ts",
					field, lookback,
				)
				rows, err := db.Query(query)
				if err != nil {
					return err
				}
				defer rows.Close()

				for rows.Next() {
					var p model.TimelinePoint
					if rows.Scan(&p.Timestamp, &p.Count) == nil {
						points = append(points, p)
					}
				}
				return nil
			})
		} else {
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

			_ = timedQuery("timeline_stats", func() error {
				rows, err := db.Query(query, args...)
				if err != nil {
					return err
				}
				defer rows.Close()

				for rows.Next() {
					var p model.TimelinePoint
					if rows.Scan(&p.Timestamp, &p.Count) == nil {
						points = append(points, p)
					}
				}
				return nil
			})
		}

		c.JSON(http.StatusOK, gin.H{"timeline": points})
	}
}
