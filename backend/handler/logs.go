package handler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"logmara/middleware"
	"logmara/model"
	"logmara/sharedstate"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

var (
	devicesCache     []model.DeviceStats
	devicesCacheMu   sync.RWMutex
	devicesCacheTime time.Time
	devicesTTL       = 60 * time.Second
)

// cacheBroadcaster is nil by default (single-server / Redis not
// configured), in which case cache invalidation stays exactly as it always
// was: local to this process only. SetCacheBroadcaster is called from
// main.go when Redis is configured, so that a purge/alias update on one
// replica invalidates every replica's cache instead of just its own.
var cacheBroadcaster *sharedstate.Broadcaster

const cacheInvalidateChannel = "cache:invalidate"

func SetCacheBroadcaster(b *sharedstate.Broadcaster) {
	cacheBroadcaster = b
}

// StartCacheInvalidationSubscriber blocks until ctx is done, applying
// invalidation events published by other replicas to this process's local
// caches. Call in its own goroutine, only after SetCacheBroadcaster.
func StartCacheInvalidationSubscriber(ctx context.Context, b *sharedstate.Broadcaster) {
	b.Subscribe(ctx, cacheInvalidateChannel, func(string) {
		invalidateAllCachesLocal()
	})
}

// InvalidateAllCaches clears this process's caches and, if Redis is
// configured, broadcasts the same invalidation to every other replica.
func InvalidateAllCaches() {
	invalidateAllCachesLocal()
	if cacheBroadcaster != nil {
		if err := cacheBroadcaster.Publish(context.Background(), cacheInvalidateChannel, ""); err != nil {
			slog.Warn("failed to broadcast cache invalidation", "error", err)
		}
	}
}

func invalidateAllCachesLocal() {
	devicesCacheMu.Lock()
	devicesCache = nil
	devicesCacheTime = time.Time{}
	devicesCacheMu.Unlock()

	statsInvalidateAll()
}

type LogQueryRequest struct {
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
	Cursor     string `json:"cursor"`
	Hostname   string `json:"hostname"`
	FromHostIP string `json:"fromhost_ip"`
	Severity   string `json:"severity"`
	AppName    string `json:"app_name"`
	Search     string `json:"search"`
	From       string `json:"from"`
	To         string `json:"to"`
	Sort       string `json:"sort"`
}

func GetLogs(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req LogQueryRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			req.Limit = 50
			req.Sort = "timestamp_desc"
		}
		if req.Limit == 0 {
			req.Limit = 50
		}
		if req.Sort == "" {
			req.Sort = "timestamp_desc"
		}

		limitInt := req.Limit
		if limitInt > MaxLogLimit {
			limitInt = MaxLogLimit
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
		whereClauses, args, argIdx := buildLogWhereClauses(opts)

		orderClause := "syslog_logs.timestamp DESC, syslog_logs.id DESC"
		cursorOp := "<"
		switch req.Sort {
		case "timestamp_asc":
			orderClause = "syslog_logs.timestamp ASC, syslog_logs.id ASC"
			cursorOp = ">"
		case "severity":
			orderClause = "syslog_logs.severity ASC, syslog_logs.timestamp DESC, syslog_logs.id DESC"
		case "hostname":
			orderClause = "syslog_logs.hostname ASC, syslog_logs.timestamp DESC, syslog_logs.id DESC"
		}

		useCursor := cursorSupported(req.Sort)
		offsetInt := req.Offset

		if useCursor && req.Cursor != "" {
			ts, id, err := decodeLogCursor(req.Cursor)
			if err != nil {
				middleware.HandleError(c, model.NewBadRequestKey("error.invalidCursor", "Invalid cursor", err))
				return
			}
			whereClauses = append(whereClauses, fmt.Sprintf("(syslog_logs.timestamp, syslog_logs.id) %s ($%d, $%d)", cursorOp, argIdx, argIdx+1))
			args = append(args, ts, id)
			argIdx += 2
			offsetInt = 0
		}

		whereSQL := buildWhereSQL(whereClauses)

		// Fetch one extra row to detect whether more pages remain, instead
		// of an exact COUNT(*) over the whole filtered result - on a large
		// table that count can cost as much as the page query itself.
		var logsQuery string
		if useCursor {
			logsQuery = fmt.Sprintf(
				"SELECT syslog_logs.id, syslog_logs.timestamp, syslog_logs.hostname, syslog_logs.fromhost_ip, syslog_logs.app_name, syslog_logs.process_id, syslog_logs.msg_id, syslog_logs.severity, syslog_logs.facility, syslog_logs.message, syslog_logs.raw_message, syslog_logs.parsed_fields, syslog_logs.matched_parsers, syslog_logs.created_at, COALESCE(da.display_name, '') "+
					"FROM syslog_logs LEFT JOIN device_aliases da ON da.fromhost_ip = COALESCE(syslog_logs.fromhost_ip, '') %s ORDER BY %s LIMIT $%d",
				whereSQL, orderClause, argIdx,
			)
			args = append(args, limitInt+1)
		} else {
			logsQuery = fmt.Sprintf(
				"SELECT syslog_logs.id, syslog_logs.timestamp, syslog_logs.hostname, syslog_logs.fromhost_ip, syslog_logs.app_name, syslog_logs.process_id, syslog_logs.msg_id, syslog_logs.severity, syslog_logs.facility, syslog_logs.message, syslog_logs.raw_message, syslog_logs.parsed_fields, syslog_logs.matched_parsers, syslog_logs.created_at, COALESCE(da.display_name, '') "+
					"FROM syslog_logs LEFT JOIN device_aliases da ON da.fromhost_ip = COALESCE(syslog_logs.fromhost_ip, '') %s ORDER BY %s LIMIT $%d OFFSET $%d",
				whereSQL, orderClause, argIdx, argIdx+1,
			)
			args = append(args, limitInt+1, offsetInt)
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), filteredQueryTimeout)
		defer cancel()
		rows, err := db.QueryContext(ctx, logsQuery, args...)
		if err != nil {
			middleware.HandleError(c, model.NewInternalKey("error.queryFailed", "Query failed", err))
			return
		}
		defer rows.Close()

		logs := scanLogRows(rows)

		hasMore := len(logs) > limitInt
		if hasMore {
			logs = logs[:limitInt]
		}

		nextCursor := ""
		if hasMore && useCursor && len(logs) > 0 {
			last := logs[len(logs)-1]
			nextCursor = encodeLogCursor(last.Timestamp, last.ID)
		}

		c.JSON(http.StatusOK, gin.H{
			"logs":        logs,
			"has_more":    hasMore,
			"next_cursor": nextCursor,
			"limit":       limitInt,
		})
	}
}

// GetLogsCount returns the exact number of rows matching the same filters as
// GetLogs. Deliberately a separate endpoint - the paginated /logs endpoint
// avoids COUNT(*) for cost reasons (see the comment there), but the sidebar
// total only needs one COUNT(*) per filter change, not per page.
func GetLogsCount(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req LogQueryRequest
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
		whereClauses, args, _ := buildLogWhereClauses(opts)
		whereSQL := buildWhereSQL(whereClauses)

		var total int64
		ctx, cancel := context.WithTimeout(c.Request.Context(), filteredQueryTimeout)
		defer cancel()
		_ = timedQuery("logs_count", func() error {
			return db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM syslog_logs %s", whereSQL), args...).Scan(&total)
		})

		c.JSON(http.StatusOK, gin.H{"total": total})
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
	// Reads from the mv_device_stats rollup (refreshed on a schedule)
	// instead of aggregating the entire syslog_logs table live on every
	// cache miss - the live GROUP BY + unnest scan over 1M+ rows was the
	// most expensive query on this endpoint.
	rows, err := db.Query(`
		SELECT d.fromhost_ip, d.hostname, d.total_logs, d.last_seen,
			d.emergency, d.alert, d.critical, d.err_count, d.warning, d.notice, d.info, d.debug,
			d.parsers, d.via_relay,
			a.display_name, a.old_hostname
		FROM mv_device_stats d
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
		var alias, oldH, viaRelay sql.NullString
		if err := rows.Scan(&ds.FromHostIP, &ds.Hostname, &total, &ds.LastSeen,
			&emergency, &alert, &critical, &errCount, &warning, &notice, &info, &debug,
			&parsersArr, &viaRelay, &alias, &oldH); err != nil {
			continue
		}
		ds.TotalLogs = total
		ds.SeverityCount = model.SeverityCounts{"emergency": emergency, "alert": alert, "critical": critical, "error": errCount, "warning": warning, "notice": notice, "info": info, "debug": debug}
		ds.MatchedParsers = parsersArr
		ds.HasParsed = len(parsersArr) > 0
		ds.ViaRelay = viaRelay.String
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
			middleware.HandleError(c, model.NewBadRequestKey("error.invalidRequest", "Invalid request", err))
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
			middleware.HandleError(c, model.NewInternalKey("logs.aliasUpdateFailed", "Failed to update device alias", err))
			return
		}
		InvalidateAllCaches()
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}
