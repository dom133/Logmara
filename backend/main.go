package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"syslytics/alertengine"
	"syslytics/audit"
	"syslytics/auth"
	"syslytics/control"
	"syslytics/db"
	"syslytics/handler"
	"syslytics/middleware"
	"syslytics/notifyhub"
	"syslytics/parser"
	"syslytics/sharedstate"
	"syslytics/tailer"

	"github.com/gin-gonic/gin"
)

// RateLimiter is satisfied by both the local, in-memory limiter (default,
// single-server/single-replica) and sharedstate.RedisRateLimiter (used
// instead when Redis is configured, so limits are shared across every api
// replica rather than reset per-process).
type RateLimiter interface {
	Allow(ip string) bool
}

// newLimiter picks the local or Redis-backed limiter based on whether
// Redis is configured. bucket namespaces the limiter's counters in Redis
// (irrelevant for the local implementation, which is only ever used by one
// process anyway). filePath is only honored for the local implementation
// (Redis already persists across restarts on its own); pass "" for limiters
// where losing counters on restart is acceptable.
func newLimiter(client *sharedstate.Client, bucket string, limit int, window time.Duration, filePath string) RateLimiter {
	if client != nil {
		return sharedstate.NewRedisRateLimiter(client, bucket, limit, window)
	}
	if filePath != "" {
		return newPersistentRateLimiter(limit, window, filePath)
	}
	return newRateLimiter(limit, window)
}

// stopIfPersistent flushes a local rate limiter's bucket state to disk
// before shutdown, so brute-force counters (login, change-password) survive
// a restart instead of silently resetting. It's a no-op for the Redis-backed
// limiter, which has no local state to flush.
func stopIfPersistent(rl RateLimiter) {
	if s, ok := rl.(interface{ Stop() }); ok {
		s.Stop()
	}
}

type persistentBucketEntry struct {
	Tokens     float64 `json:"t"`
	LastRefill int64   `json:"lr"`
}

type tokenBucketEntry struct {
	tokens     float64
	lastRefill time.Time
}

type rateLimiter struct {
	mu         sync.Mutex
	buckets    map[string]*tokenBucketEntry
	limit      int
	window     time.Duration
	refillRate float64
	filePath   string
	stop       chan struct{}
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{
		buckets:    make(map[string]*tokenBucketEntry),
		limit:      limit,
		window:     window,
		refillRate: float64(limit) / window.Seconds(),
		stop:       make(chan struct{}),
	}
	go rl.saveLoop()
	return rl
}

func newPersistentRateLimiter(limit int, window time.Duration, filePath string) *rateLimiter {
	rl := &rateLimiter{
		buckets:    make(map[string]*tokenBucketEntry),
		limit:      limit,
		window:     window,
		refillRate: float64(limit) / window.Seconds(),
		filePath:   filePath,
		stop:       make(chan struct{}),
	}
	rl.loadState()
	go rl.saveLoop()
	return rl
}

func (rl *rateLimiter) loadState() {
	if rl.filePath == "" {
		return
	}
	data, err := os.ReadFile(rl.filePath)
	if err != nil {
		return
	}
	var entries map[string]persistentBucketEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return
	}
	now := time.Now()
	for ip, e := range entries {
		age := now.Sub(time.Unix(e.LastRefill, 0))
		if age > rl.window {
			continue
		}
		rl.buckets[ip] = &tokenBucketEntry{
			tokens:     e.Tokens + age.Seconds()*rl.refillRate,
			lastRefill: now,
		}
		if rl.buckets[ip].tokens > float64(rl.limit) {
			rl.buckets[ip].tokens = float64(rl.limit)
		}
	}
}

func (rl *rateLimiter) saveState() {
	if rl.filePath == "" {
		return
	}
	entries := make(map[string]persistentBucketEntry)
	for ip, e := range rl.buckets {
		entries[ip] = persistentBucketEntry{
			Tokens:     e.tokens,
			LastRefill: e.lastRefill.Unix(),
		}
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return
	}
	_ = os.WriteFile(rl.filePath, data, 0600)
}

func (rl *rateLimiter) Stop() {
	rl.saveState()
	close(rl.stop)
}

func (rl *rateLimiter) saveLoop() {
	ticker := time.NewTicker(rl.window)
	defer ticker.Stop()
	for {
		select {
		case <-rl.stop:
			return
		case <-ticker.C:
			rl.mu.Lock()
			for ip, entry := range rl.buckets {
				if entry.tokens >= float64(rl.limit) {
					delete(rl.buckets, ip)
				}
			}
			rl.mu.Unlock()
			if rl.filePath != "" {
				rl.saveState()
			}
		}
	}
}

func (rl *rateLimiter) refill(entry *tokenBucketEntry, now time.Time) {
	elapsed := now.Sub(entry.lastRefill).Seconds()
	entry.tokens += elapsed * rl.refillRate
	if entry.tokens > float64(rl.limit) {
		entry.tokens = float64(rl.limit)
	}
	entry.lastRefill = now
}

func (rl *rateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	entry, exists := rl.buckets[ip]
	if !exists {
		entry = &tokenBucketEntry{
			tokens:     float64(rl.limit) - 1,
			lastRefill: now,
		}
		rl.buckets[ip] = entry
		return true
	}

	rl.refill(entry, now)
	if entry.tokens < 1 {
		return false
	}
	entry.tokens--
	return true
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// sharedClient is nil unless REDIS_SENTINEL_ADDRS/REDIS_ADDR are set -
	// that's the expected, fully-supported single-server path, where every
	// piece of shared state below falls back to its original in-memory/
	// single-process behavior. A non-nil error here means Redis *is*
	// configured but unreachable, which is fatal: running multiple api
	// replicas without working coordination (rate limits, tailer leader
	// election) is unsafe, so fail fast rather than silently degrade.
	sharedClient, err := sharedstate.Connect()
	if err != nil {
		slog.Error("redis configured but unreachable", "error", err)
		os.Exit(1)
	}
	if sharedClient != nil {
		defer sharedClient.Close()
		slog.Info("redis shared state enabled")
	}

	// Feed every slow query recorded at the driver level (db.instrumentedConn)
	// into the same admin slow-query log that handler.timedQuery writes to,
	// so /admin/slow-queries covers all database access, not just the
	// call sites explicitly wrapped in timedQuery.
	db.SetSlowQueryHook(handler.RecordSlowQuery)

	dsn := os.Getenv("DATABASE_URL")

	var database *sql.DB
	if dsn != "" {
		d, err := db.Connect(dsn)
		if err != nil {
			slog.Error("failed to connect to database", "error", err)
			os.Exit(1)
		}
		database = d
	} else {
		slog.Info("DATABASE_URL not set; serving the setup wizard until database settings are submitted")
		database = waitForWizardDatabase(port, sharedClient)
	}
	defer database.Close()

	db.SetAppStarting(true)
	schemaReady := make(chan struct{})
	migrationDone := make(chan struct{})
	go func() {
		defer close(migrationDone)
		if err := db.MigrateWithLock(database); err != nil {
			slog.Error("failed to migrate database", "error", err)
			os.Exit(1)
		}
		close(schemaReady)

		// RefreshMaterializedViews scans the full syslog_logs table and its
		// runtime grows with log volume, unlike MigrateWithLock above (a fast
		// no-op once the schema exists). It must not gate the HTTP listener
		// (and therefore /api/health) from coming up, or every restart's
		// health-check window scales with how much data has accumulated -
		// auth/routes/tailer below only need the schema, not fresh views.
		db.RefreshMaterializedViews(database)
		db.ApplyEnvSettingOverrides(database)
		db.SetAppStarting(false)
		slog.Info("database migration and initialization complete")
	}()

	// The frontend container only depends_on api starting (not api being
	// healthy), and api starts well before frontend's entrypoint has
	// generated its cert and brought up the reload sidecar - so a sync
	// right after migration reliably hits connection-refused on a cold
	// `docker compose up`. Retry in the background instead of blocking
	// startup on it; nginx already defaults to HTTPS-off, so this only
	// matters for applying an env-var override or a state left over from
	// before a restart.
	go func() {
		<-migrationDone
		const attempts = 10
		const delay = 3 * time.Second
		if err := handler.SyncNginxHTTPSWithRetry(database, attempts, delay); err != nil {
			slog.Warn("failed to sync nginx HTTPS config at startup after retries", "attempts", attempts, "error", err)
		}
	}()

	// Same reasoning as the nginx sync above: the rsyslog container's reload
	// sidecar (see rsyslog/reload-sidecar) may not be up yet on a cold
	// `docker compose up`, and /data/relay's PKI material + ACL live on the
	// shared volume, not in the database, so they need to be re-applied on
	// every restart regardless of whether relay_ingestion_enabled changed.
	go func() {
		<-migrationDone
		const attempts = 10
		const delay = 3 * time.Second
		if err := handler.SyncRelayConfigWithRetry(database, attempts, delay); err != nil {
			slog.Warn("failed to sync relay config at startup after retries", "attempts", attempts, "error", err)
		}
	}()

	ctx, maintCancel := context.WithCancel(context.Background())
	stopVacuum, stopMV, stopTokenCleanup, stopJWTCleanup := db.StartMaintenance(ctx, database)
	_ = stopJWTCleanup

	// Fast MV refresh for dashboard_summary (every 30s) to keep stats responsive
	// while someone is actually logged in to look at them. With nobody logged
	// in, this tick is a single cheap EXISTS check against refresh_tokens
	// instead of a full REFRESH MATERIALIZED VIEW CONCURRENTLY pass - the
	// 30-minute scheduler in db.StartMaintenance keeps running regardless, so
	// the views never go stale for more than that. RefreshMV itself skips
	// while migration is in progress, so it's safe to start this ticker
	// immediately.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		slog.Info("fast dashboard MV refresh started", "interval_seconds", 30)
		for {
			select {
			case <-ctx.Done():
				slog.Info("fast dashboard MV refresh stopped")
				return
			case <-ticker.C:
				if db.HasActiveSession(database) {
					db.RefreshMV(database)
				}
			}
		}
	}()

	// auth.Init/parser.NewEngine/the tailer all query tables that only exist
	// once db.Migrate has run (users, parsers, syslog_logs) - on a brand new
	// database these queries can otherwise race ahead of table creation and
	// fail. They don't need RefreshMaterializedViews/ApplyEnvSettingOverrides
	// to have finished too, so this waits on schemaReady (closed right after
	// the schema migration) rather than the fuller migrationDone - keeping
	// the HTTP listener off the critical path of the materialized-view
	// refresh below.
	<-schemaReady

	// Validate TLS configuration: warn if HTTPS is enabled but certificates are missing
	if tlsEnabled := db.GetSetting(database, "https_enabled", "false"); tlsEnabled == "true" {
		certPath := os.Getenv("TLS_CERT_PATH")
		if certPath == "" {
			certPath = "/etc/ssl/certs/syslytics/fullchain.pem"
		}
		keyPath := os.Getenv("TLS_KEY_PATH")
		if keyPath == "" {
			keyPath = "/etc/ssl/private/syslytics/privkey.pem"
		}
		if _, err := os.Stat(certPath); err != nil {
			slog.Warn("https_enabled is true but TLS certificate not found", "path", certPath)
		}
		if _, err := os.Stat(keyPath); err != nil {
			slog.Warn("https_enabled is true but TLS key not found", "path", keyPath)
		}
	}

	authCfg, err := auth.Init(database)
	if err != nil {
		slog.Error("auth initialization failed", "error", err)
		os.Exit(1)
	}

	engine := parser.NewEngine(database)
	ic := control.New(ctx, sharedClient)

	// With Redis configured, cache invalidation and the slow-query log get
	// shared across replicas instead of staying local to whichever replica
	// handled the triggering request; the tailer gets a leader elector so
	// exactly one replica actively ingests at a time. All of this is a
	// no-op when sharedClient is nil.
	var elector *sharedstate.LeaderElector
	if sharedClient != nil {
		broadcaster := sharedstate.NewBroadcaster(sharedClient)
		handler.SetCacheBroadcaster(broadcaster)
		go handler.StartCacheInvalidationSubscriber(ctx, broadcaster)
		handler.SetSlowQueryStore(sharedClient)
		elector = sharedstate.NewLeaderElector(sharedClient, "tailer", 15*time.Second)
	}

	logFilePath := os.Getenv("LOG_FILE_PATH")
	if logFilePath == "" {
		logFilePath = "/data/logs.jsonl"
	}
	alertEngine := alertengine.NewEngine(database, sharedClient)
	audit.SetAlertEngine(alertEngine)
	notifHub := notifyhub.NewHub(ctx, sharedClient)
	alertEngine.SetOnInApp(notifHub.Publish)
	go tailer.Run(ctx, database, logFilePath, engine, ic, elector, alertEngine)

	// Device silence checks run independently on every replica: the read
	// (mv_device_stats) is cheap and the per-rule-per-device cooldown key in
	// alertEngine's counter store (Redis-backed when configured) already
	// dedupes duplicate fires, the same way vacuum/MV refresh above already
	// run redundantly on every replica without an elector.
	silenceCheckMin := 5
	if v, err := strconv.Atoi(db.GetSetting(database, "device_silence_check_minutes", "5")); err == nil && v > 0 {
		silenceCheckMin = v
	}
	go func() {
		ticker := time.NewTicker(time.Duration(silenceCheckMin) * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				alertEngine.CheckDeviceSilence(database)
			}
		}
	}()

	// Relay certificate expiry: hourly is more than enough resolution for a
	// day-granularity warning window, and piggybacking SyncRelayConfig here
	// (not just the expiry check) means the CA/server certificate's own
	// in-place renewal (see relaypki.EnsureCA) keeps happening on a schedule
	// even on a deployment that goes untouched for long stretches between
	// relay whitelist/certificate changes and restarts.
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := handler.SyncRelayConfig(database); err != nil {
					slog.Warn("relay config sync failed during periodic check", "error", err)
				}
				alertEngine.CheckRelayCertExpiring(database)
			}
		}
	}()

r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.ErrorHandler())
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.GzipCompress())
	r.Use(middleware.ETag())
	// CORS is handled entirely by the frontend's nginx reverse proxy (see
	// frontend/nginx.conf and handler.reloadNginx) - clients only ever reach
	// this API through it, so there's no CORS handling here.

	// login and change-password guard against credential brute-forcing, so
	// their counters are persisted to the /data volume and survive an api
	// restart; the rest are low-stakes enough that losing counters on
	// restart is an acceptable tradeoff for the extra complexity.
	loginLimiter := newLimiter(sharedClient, "login", 5, time.Minute, "/data/ratelimit-login.json")
	refreshLimiter := newLimiter(sharedClient, "refresh", 10, time.Minute, "")
	initLimiter := newLimiter(sharedClient, "init", 3, time.Hour, "")
	changePasswordLimiter := newLimiter(sharedClient, "change-password", 5, time.Minute, "/data/ratelimit-change-password.json")

	r.GET("/api/health", handler.HealthCheck(database))
	r.GET("/api/metrics", handler.PrometheusMetrics(database))
	r.GET("/api/metrics", handler.PrometheusMetrics(database))
	r.POST("/api/auth/login", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), rateLimitMiddleware(loginLimiter), handler.Login(database, authCfg))
	r.POST("/api/auth/refresh", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), rateLimitMiddleware(refreshLimiter), handler.Refresh(database, authCfg))
	r.POST("/api/auth/logout", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.Logout(database))
	r.GET("/api/status/initialized", handler.CheckInitialized(database))
	r.POST("/api/init", middleware.RequireJSON(), middleware.MaxRequestBodySize(8*1024), rateLimitMiddleware(initLimiter), handler.Initialize(database))
	r.GET("/api/init/generate-keys", handler.GenerateKeys())
	r.GET("/api/init/db-config", handler.GetDbConfig())

	authGroup := r.Group("/api")
	authGroup.Use(authCfg.JWTRequired())
	authGroup.Use(handler.CSRFRequired())
	{
		authGroup.POST("/logs", handler.GetLogs(database))
		authGroup.POST("/logs/count", handler.GetLogsCount(database))

		authGroup.GET("/stats/dashboard", handler.GetDashboardStats(database))
		authGroup.GET("/stats/devices", handler.GetDeviceStats(database))
		authGroup.GET("/stats/severity", handler.GetSeverityStats(database))
		authGroup.GET("/stats/timeline", handler.GetTimelineStats(database))
		authGroup.GET("/devices", handler.GetDevices(database))
		authGroup.GET("/export/csv", handler.ExportCSV(database))
		authGroup.GET("/export/html", handler.ExportHTML(database))
		authGroup.GET("/auth/me", handler.GetMe(database))
		authGroup.POST("/auth/change-password", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), rateLimitMiddleware(changePasswordLimiter), handler.ChangePassword(database))

		notificationsGate := handler.RequireNotificationsEnabled(database)

		// Deliberately ungated: the bell needs GET /notifications to reach it
		// even while disabled, since that's how it learns enabled:false and
		// hides itself. mark-read is harmless either way.
		authGroup.GET("/notifications", handler.GetNotifications(database))
		authGroup.POST("/notifications/mark-read", handler.MarkNotificationsRead(database))
		authGroup.GET("/notifications/stream", notificationsGate, handler.StreamNotifications(notifHub))

		authGroup.GET("/push/vapid-public-key", notificationsGate, handler.GetVAPIDPublicKey(database))
		authGroup.POST("/push/subscribe", notificationsGate, handler.SubscribePush(database))
		authGroup.POST("/push/unsubscribe", notificationsGate, handler.UnsubscribePush(database))

		authGroup.GET("/parsers", handler.ListParsers(engine))
		authGroup.GET("/parsers/fields", handler.ListParsedFields(engine))
		authGroup.GET("/dashboards", handler.ListDashboards(database))
		authGroup.GET("/dashboards/:id", handler.GetDashboard(database))
		authGroup.GET("/dashboards/:id/data", handler.GetDashboardData(database))
		authGroup.GET("/dashboards/:id/count", handler.GetDashboardDataCount(database))
		authGroup.GET("/dashboards/:id/export/csv", handler.ExportDashboardCSV(database))
		authGroup.GET("/dashboards/:id/export/html", handler.ExportDashboardHTML(database))
		authGroup.PATCH("/dashboards/:id/pin", handler.TogglePinDashboard(database))

		editorGroup := authGroup.Group("")
		editorGroup.Use(auth.RoleRequired("admin", "editor"))
		{
			editorGroup.POST("/parsers", handler.CreateParser(engine))
			editorGroup.PUT("/parsers/:id", handler.UpdateParser(engine))
			editorGroup.DELETE("/parsers/:id", handler.DeleteParser(engine))
			editorGroup.POST("/parsers/:id/clone", handler.CloneParser(engine))
			editorGroup.POST("/parsers/test", handler.TestParser(engine))
			editorGroup.POST("/parsers/reparse", handler.ReparseUnparsed(engine))

			editorGroup.POST("/dashboards", handler.CreateDashboard(database))
			editorGroup.PUT("/dashboards/:id", handler.UpdateDashboard(database))
			editorGroup.DELETE("/dashboards/:id", handler.DeleteDashboard(database))
			editorGroup.PATCH("/dashboards/:id/public", handler.TogglePublicDashboard(database))

			editorGroup.GET("/alerts", notificationsGate, handler.ListAlerts(database))
			editorGroup.POST("/alerts", notificationsGate, handler.CreateAlert(database))
			editorGroup.PUT("/alerts/:id", notificationsGate, handler.UpdateAlert(database))
			editorGroup.DELETE("/alerts/:id", notificationsGate, handler.DeleteAlert(database))
		}

		adminGroup := authGroup.Group("/admin")
		adminGroup.Use(auth.AdminRequired())
		{
			adminGroup.GET("/users", handler.ListUsers(database))
			adminGroup.POST("/users", handler.CreateUser(database))
			adminGroup.PUT("/users/:id", handler.UpdateUser(database))
			adminGroup.DELETE("/users/:id", handler.DeleteUser(database))
			adminGroup.PUT("/users/:id/reset-password", handler.ResetPassword(database))
			adminGroup.POST("/users/:id/unlock", handler.UnlockUserHandler(database))
			adminGroup.GET("/settings", handler.GetSettings(database))
			adminGroup.PUT("/settings", handler.UpdateSettings(database))
			adminGroup.POST("/settings/cleanup", handler.CleanupLogs(database))
			adminGroup.DELETE("/logs", handler.PurgeAllLogs(database, ic))
			adminGroup.POST("/ingestion/pause", handler.PauseIngestion(ic))
			adminGroup.POST("/ingestion/resume", handler.ResumeIngestion(ic))
			adminGroup.GET("/ingestion/status", handler.GetIngestionStatus(ic))
			adminGroup.POST("/ldap/test", handler.TestLDAP(database))
			adminGroup.GET("/audit-log", handler.GetAuditLog(database))
			adminGroup.GET("/slow-queries", handler.GetSlowQueries())
			adminGroup.DELETE("/slow-queries", handler.ClearSlowQueriesHandler())
			adminGroup.GET("/health/containers", handler.GetContainersHealth(database))
			adminGroup.PUT("/devices/:ip/alias", handler.UpdateDeviceAlias(database))
			adminGroup.POST("/ssl/upload", handler.UploadSSLCerts(database))
			adminGroup.POST("/nginx-reload", handler.ReloadNginx(database))

			adminGroup.GET("/relay/whitelist", handler.ListRelayWhitelist(database))
			adminGroup.POST("/relay/whitelist", handler.CreateRelayWhitelistEntry(database))
			adminGroup.DELETE("/relay/whitelist/:id", handler.DeleteRelayWhitelistEntry(database))
			adminGroup.POST("/relay/whitelist/:id/certificate", handler.GenerateCertificateForWhitelistEntry(database))
			adminGroup.GET("/relay/certificates", handler.ListRelayCertificates(database))
			adminGroup.POST("/relay/certificates", handler.CreateRelayCertificate(database))
			adminGroup.DELETE("/relay/certificates/:id", handler.RevokeRelayCertificate(database))
			adminGroup.POST("/relay/certificates/:id/regenerate", handler.RegenerateRelayCertificate(database))

			adminGroup.POST("/notification-channels", notificationsGate, handler.CreateNotificationChannel(database))
			adminGroup.PUT("/notification-channels/:id", notificationsGate, handler.UpdateNotificationChannel(database))
			adminGroup.DELETE("/notification-channels/:id", notificationsGate, handler.DeleteNotificationChannel(database))
			adminGroup.POST("/notification-channels/:id/test", notificationsGate, handler.TestNotificationChannel(database, notifHub))
			adminGroup.DELETE("/notifications/history", notificationsGate, handler.ClearNotificationHistory(database))
		}

		// Same /admin path prefix as adminGroup above, but readable by editors
		// too - they can already create alert rules (editorGroup), so they
		// need to see which channels exist to assign and whether their rules
		// actually fired. Channel secrets and mutations stay admin-only.
		adminReadGroup := authGroup.Group("/admin")
		adminReadGroup.Use(auth.RoleRequired("admin", "editor"))
		{
			adminReadGroup.GET("/notification-channels", notificationsGate, handler.ListNotificationChannels(database))
			adminReadGroup.GET("/notifications/history", notificationsGate, handler.GetNotificationHistory(database))
		}
	}

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("server starting", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down server...")
	maintCancel()
	stopVacuum()
	stopMV()
	stopTokenCleanup()
	stopIfPersistent(loginLimiter)
	stopIfPersistent(changePasswordLimiter)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("forced shutdown", "error", err)
	}
	slog.Info("server stopped")
}

// waitForWizardDatabase runs a minimal, database-less HTTP server exposing
// only the setup wizard's endpoints, and blocks until the wizard submits
// working database settings. It returns the resulting live connection so
// main() can continue its normal startup sequence on it.
func waitForWizardDatabase(port string, sharedClient *sharedstate.Client) *sql.DB {
	ready := make(chan *sql.DB, 1)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.ErrorHandler())
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.GzipCompress())

	initLimiter := newLimiter(sharedClient, "wizard-init", 3, time.Hour, "")
	testDbLimiter := newLimiter(sharedClient, "wizard-test-db", 20, 10*time.Minute, "")
	r.GET("/api/health", handler.HealthCheckStandalone())
	r.GET("/api/status/initialized", handler.CheckInitializedStandalone())
	r.GET("/api/init/generate-keys", handler.GenerateKeys())
	r.GET("/api/init/db-config", handler.GetDbConfig())
	r.POST("/api/init/test-db", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), rateLimitMiddleware(testDbLimiter), handler.TestDatabaseConfig())
	r.POST("/api/init", middleware.RequireJSON(), middleware.MaxRequestBodySize(8*1024), rateLimitMiddleware(initLimiter), handler.InitializeStandalone(ready))

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("setup wizard server starting", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("setup wizard server failed", "error", err)
			os.Exit(1)
		}
	}()

	database := <-ready
	slog.Info("database settings received from setup wizard, handing off to the main server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("setup wizard server did not shut down cleanly", "error", err)
	}

	return database
}

func rateLimitMiddleware(rl RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if !rl.Allow(ip) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many login attempts, try again later"})
			c.Abort()
			return
		}
		c.Next()
	}
}
