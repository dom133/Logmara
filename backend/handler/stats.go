package handler

import (
	"database/sql"
	"fmt"
	"net/http"

	"syslog-gui/model"

	"github.com/gin-gonic/gin"
)

func GetDashboardStats(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var stats model.DashboardStats

		db.QueryRow("SELECT COUNT(*) FROM syslog_logs").Scan(&stats.TotalLogs)

		db.QueryRow(
			"SELECT COUNT(*) FROM syslog_logs WHERE timestamp >= NOW() - INTERVAL '1 hour'",
		).Scan(&stats.LogsLastHour)

		db.QueryRow(
			"SELECT COUNT(*) FROM syslog_logs WHERE timestamp >= NOW() - INTERVAL '1 day'",
		).Scan(&stats.LogsLastDay)

		db.QueryRow("SELECT COUNT(DISTINCT hostname) FROM syslog_logs").Scan(&stats.UniqueDevices)

		stats.SeverityCounts = make(map[string]int64)
		rows, err := db.Query("SELECT severity, COUNT(*) FROM syslog_logs GROUP BY severity ORDER BY COUNT(*) DESC")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var sev string
				var cnt int64
				if rows.Scan(&sev, &cnt) == nil {
					stats.SeverityCounts[sev] = cnt
				}
			}
		}

		rows, err = db.Query("SELECT COALESCE(MIN(fromhost_ip), ''), MIN(hostname) as hostname, COUNT(*) as cnt FROM syslog_logs GROUP BY fromhost_ip ORDER BY cnt DESC LIMIT 10")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var d model.DeviceCount
				if rows.Scan(&d.FromHostIP, &d.Hostname, &d.Count) == nil {
					stats.TopDevices = append(stats.TopDevices, d)
				}
			}
		}

		if stats.TopDevices == nil {
			stats.TopDevices = []model.DeviceCount{}
		}

		rows, err = db.Query(
			"SELECT substring(message from 1 for 100) as msg, hostname, COUNT(*) as cnt " +
				"FROM syslog_logs WHERE severity IN ('err', 'crit', 'alert', 'emerg') " +
				"GROUP BY msg, hostname ORDER BY cnt DESC LIMIT 10",
		)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var e model.ErrorMessage
				if rows.Scan(&e.Message, &e.Hostname, &e.Count) == nil {
					stats.TopErrors = append(stats.TopErrors, e)
				}
			}
		}

		if stats.TopErrors == nil {
			stats.TopErrors = []model.ErrorMessage{}
		}

		c.JSON(http.StatusOK, stats)
	}
}

func GetDeviceStats(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := db.Query(
			"SELECT COALESCE(MIN(fromhost_ip), ''), MIN(hostname) as hostname, COUNT(*) as total, MAX(timestamp) as last_seen " +
				"FROM syslog_logs GROUP BY fromhost_ip ORDER BY total DESC",
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Query failed"})
			return
		}
		defer rows.Close()

		var devices []model.DeviceStats
		for rows.Next() {
			var d model.DeviceStats
			if err := rows.Scan(&d.FromHostIP, &d.Hostname, &d.TotalLogs, &d.LastSeen); err != nil {
				continue
			}

			d.SeverityCount = make(model.SeverityCounts)
			sevRows, _ := db.Query(
				"SELECT severity, COUNT(*) FROM syslog_logs WHERE COALESCE(fromhost_ip, '') = $1 GROUP BY severity",
				d.FromHostIP,
			)
			if sevRows != nil {
				for sevRows.Next() {
					var sev string
					var cnt int64
					if sevRows.Scan(&sev, &cnt) == nil {
						d.SeverityCount[sev] = cnt
					}
				}
				sevRows.Close()
			}

			devices = append(devices, d)
		}

		if devices == nil {
			devices = []model.DeviceStats{}
		}

		c.JSON(http.StatusOK, gin.H{"devices": devices})
	}
}

func GetSeverityStats(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		from := c.Query("from")
		to := c.Query("to")

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
			argIdx++
		}

		rows, err := db.Query(
			"SELECT severity, COUNT(*) FROM syslog_logs"+where+" GROUP BY severity ORDER BY COUNT(*) DESC",
			args...,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Query failed"})
			return
		}
		defer rows.Close()

		var stats []model.SeverityStats
		for rows.Next() {
			var s model.SeverityStats
			if rows.Scan(&s.Severity, &s.Count) == nil {
				stats = append(stats, s)
			}
		}

		c.JSON(http.StatusOK, gin.H{"stats": stats})
	}
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

		query := "SELECT date_trunc($1, timestamp) as ts, COUNT(*) FROM syslog_logs"
		args := []interface{}{field}
		argIdx := 2

		if from != "" {
			query += fmt.Sprintf(" WHERE timestamp >= $%d", argIdx)
			args = append(args, from)
			argIdx++
		}
		if to != "" {
			if from == "" {
				query += " WHERE"
			} else {
				query += " AND"
			}
			query += fmt.Sprintf(" timestamp <= $%d", argIdx)
			args = append(args, to)
			argIdx++
		}

		query += " GROUP BY ts ORDER BY ts"

		rows, err := db.Query(query, args...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Query failed"})
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