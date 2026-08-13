package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"logmara/alertengine"
	"logmara/audit"
	"logmara/auth"
	"logmara/control"
	"logmara/db"
	"logmara/handler"
	"logmara/middleware"
	"logmara/notifyhub"
	"logmara/parser"
	"logmara/sharedstate"
	"logmara/tailer"
	"logmara/util"
	"logmara/vaultclient"

	"github.com/gin-gonic/gin"
)

const appVersion = "0.0.3"

func versionHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"version": appVersion})
}

// defaultLanguageHandler exposes only the configured default UI language,
// unauthenticated, so the login page and setup wizard can pick it before
// anyone has signed in. It intentionally never touches handler.GetSettings
// (the admin-only endpoint), which returns internal config - SMTP/LDAP
// hosts, CORS origins, session limits - that must not be public.
func defaultLanguageHandler(pool *db.DynamicPool) gin.HandlerFunc {
	return func(c *gin.Context) {
		lang := "en"
		if pool != nil {
			lang = db.GetSetting(pool.Get(), "default_language", "en")
		}
		c.JSON(http.StatusOK, gin.H{"default_language": lang})
	}
}

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

	// --migrate-only runs just the DB schema migration plus builtin
	// parser/settings seeding, then exits - for manually applying a
	// migration (e.g. via `docker exec <api container> ./server
	// --migrate-only`) without waiting on a full service restart/rolling
	// update and its healthcheck start-period.
	migrateOnly := flag.Bool("migrate-only", false, "run pending DB migration and builtin parser/settings seeding, then exit")
	flag.Parse()
	if *migrateOnly {
		if err := vaultclient.Get().WaitUntilReady(); err != nil {
			slog.Error("vault not ready", "error", err)
			os.Exit(1)
		}
		dsn := util.ResolveDatabaseURL()
		if dsn == "" {
			slog.Error("DATABASE_URL not set; cannot run --migrate-only")
			os.Exit(1)
		}
		database, err := db.Connect(dsn)
		if err != nil {
			slog.Error("failed to connect to database", "error", err)
			os.Exit(1)
		}
		defer database.Close()
		if err := db.MigrateWithLock(database); err != nil {
			slog.Error("migration failed", "error", err)
			os.Exit(1)
		}
		slog.Info("migrate-only: schema migration and builtin parser/settings seeding complete")
		return
	}

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

	// API_REPLICAS mirrors the deploy.replicas value docker-stack.app.yml
	// passes through as an env var (see its "environment:" block) - it has
	// no effect on the single-server docker-compose.yml path, which never
	// sets it and defaults to 1 here. Running more than one api replica
	// without Redis means every replica runs its own uncoordinated tailer
	// against the same shared log file and position checkpoint (see
	// tailer.Run / runIngestionLoop): they race on both, corrupting the
	// checkpoint and producing MALFORMED JSON as one replica seeks the file
	// mid-line. That's silent data corruption, not just a missed
	// optimization, so fail fast instead of letting it happen.
	apiReplicas := 1
	if v, err := strconv.Atoi(os.Getenv("API_REPLICAS")); err == nil && v > 0 {
		apiReplicas = v
	}
	if apiReplicas > 1 && sharedClient == nil {
		slog.Error("API_REPLICAS > 1 without Redis configured; multiple replicas would run uncoordinated tailers against the same log file and corrupt its position checkpoint - deploy docker-stack.redis.yml and set REDIS_SENTINEL_ADDRS/REDIS_ADDR, or run a single replica", "api_replicas", apiReplicas)
		os.Exit(1)
	}

	// Feed every slow query recorded at the driver level (db.instrumentedConn)
	// into the same admin slow-query log that handler.timedQuery writes to,
	// so /admin/slow-queries covers all database access, not just the
	// call sites explicitly wrapped in timedQuery.
	db.SetSlowQueryHook(handler.RecordSlowQuery)

	// Supports DATABASE_URL_FILE, or POSTGRES_HOST + POSTGRES_PASSWORD_FILE,
	// as well as the plain DATABASE_URL env var, so a Swarm deployment can
	// keep the DB password out of the deploy-time environment - see
	// util.ResolveDatabaseURL.
	// If Vault is configured, wait for it to become unsealed first.
	// ResolveDatabaseURL pulls the DB password from Vault, so attempting
	// connection before Vault is ready always fails with an empty password.
	if err := vaultclient.Get().WaitUntilReady(); err != nil {
		slog.Error("vault not ready", "error", err)
		os.Exit(1)
	}

	dsn := util.ResolveDatabaseURL()

	var dynamicPool *db.DynamicPool
	if dsn != "" {
		pool, err := db.NewDynamicPool(dsn)
		if err != nil {
			slog.Error("failed to connect to database", "error", err)
			os.Exit(1)
		}
		dynamicPool = pool
	} else {
		slog.Info("DATABASE_URL not set; serving the setup wizard until database settings are submitted")
		dynamicPool = waitForWizardDatabase(port, sharedClient)
	}
	defer dynamicPool.Close()

	db.SetAppStarting(true)
	schemaReady := make(chan struct{})
	migrationDone := make(chan struct{})

	// The real router/listener below doesn't come up until schemaReady
	// closes (see the <-schemaReady wait further down) - a schema migration
	// on a large existing database can run for minutes, and until this stub
	// existed that whole window left /api/health completely unreachable
	// (connection refused, not even "starting"), not just slow. Docker's
	// HEALTHCHECK then had nothing to succeed against, so a long enough
	// migration flipped the container to unhealthy before it ever finished -
	// which a Swarm deployment's health-based task monitor (or any external
	// restarter watching container health) reacts to by killing and
	// restarting the task, aborting the migration mid-flight and starting it
	// over from scratch every time. This stub server exists solely to answer
	// /api/health with "starting" (still HTTP 200, since db.IsAppStarting()
	// is true) for that window, and is torn down right after schemaReady
	// closes, before the real server binds the same port.
	startupSrv := &http.Server{
		Addr:         ":" + port,
		Handler:      startupHealthHandler(dynamicPool),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	go func() {
		if err := startupSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("startup health server failed", "error", err)
		}
	}()

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(migrationDone)
		if err := db.MigrateWithLock(dynamicPool.Get()); err != nil {
			slog.Error("failed to migrate database", "error", err)
			os.Exit(1)
		}
		close(schemaReady)
	}()

	// RefreshMaterializedViews scans the full syslog_logs table and its
	// runtime grows with log volume - mv_device_stats alone has been
	// observed taking 10+ minutes on a large table. It must not gate
	// anything downstream of the schema itself: not the HTTP listener, not
	// migrationDone (and therefore not nginx/relay sync or the tailer's
	// leader-election wait below), none of which need fresh views or the
	// settings overrides applied to do their job. Runs untracked by wg, off
	// every one of those critical paths, once the schema alone is ready.
	go func() {
		<-schemaReady
		db.RefreshMaterializedViews(dynamicPool.Get())
		db.ApplyEnvSettingOverrides(dynamicPool.Get())
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
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-migrationDone
		const attempts = 10
		const delay = 3 * time.Second
		if err := handler.SyncNginxHTTPSWithRetry(dynamicPool, attempts, delay); err != nil {
			slog.Warn("failed to sync nginx HTTPS config at startup after retries", "attempts", attempts, "error", err)
		}
	}()

	// Same reasoning as the nginx sync above: the rsyslog container's reload
	// sidecar (see rsyslog/reload-sidecar) may not be up yet on a cold
	// `docker compose up`, and /data/relay's PKI material + ACL live on the
	// shared volume, not in the database, so they need to be re-applied on
	// every restart regardless of whether relay_ingestion_enabled changed.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-migrationDone
		const attempts = 10
		const delay = 3 * time.Second
		if err := handler.SyncRelayConfigWithRetry(dynamicPool, attempts, delay); err != nil {
			slog.Warn("failed to sync relay config at startup after retries", "attempts", attempts, "error", err)
		}
	}()

	ctx, maintCancel := context.WithCancel(context.Background())
	stopVacuum, stopMV, stopTokenCleanup, stopJWTCleanup, stopArchiveCleanup, stopPartitions := db.StartMaintenance(ctx, dynamicPool.Get())
	_ = stopJWTCleanup
	_ = stopArchiveCleanup

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
				if db.HasActiveSession(dynamicPool.Get()) {
					db.RefreshMV(ctx, dynamicPool.Get())
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
	if tlsEnabled := db.GetSetting(dynamicPool.Get(), "https_enabled", "false"); tlsEnabled == "true" {
		certPath := os.Getenv("TLS_CERT_PATH")
		if certPath == "" {
			certPath = "/data/ssl/server.crt"
		}
		keyPath := os.Getenv("TLS_KEY_PATH")
		if keyPath == "" {
			keyPath = "/data/ssl/server.key"
		}
		if _, err := os.Stat(certPath); err != nil {
			slog.Warn("https_enabled is true but TLS certificate not found", "path", certPath)
		}
		if _, err := os.Stat(keyPath); err != nil {
			slog.Warn("https_enabled is true but TLS key not found", "path", keyPath)
		}
	}

	authCfg, err := auth.Init(dynamicPool)
	if err != nil {
		slog.Error("auth initialization failed", "error", err)
		os.Exit(1)
	}

	// The encryption key (like the JWT secret validated by auth.Init above)
	// comes only from the environment and is never stored in the database, so
	// a database dump alone can't decrypt stored SMTP/LDAP credentials. Fail
	// fast if it's missing rather than silently returning ciphertext later.
	encKey := util.SecretFromEnv("ENCRYPTION_KEY")
	if encKey == "" {
		slog.Error("ENCRYPTION_KEY is not set; generate one (e.g. `openssl rand -base64 48`) and provide it via ENCRYPTION_KEY or ENCRYPTION_KEY_FILE - see README")
		os.Exit(1)
	}
	util.SetEncryptionKey(encKey)

	// Start secret rotation goroutine (24h interval). StartRotation blocks
	// in an infinite loop until ctx is cancelled - it must run in its own
	// goroutine, or main() never reaches wg.Wait()/the tailer/the real
	// HTTP server below, leaving /api/health stuck reporting "starting"
	// forever.
	vc := vaultclient.Get()
	go vc.StartRotation(ctx, vaultclient.RotationCallbacks{
		RotateJWTSecret:     func(s string) { authCfg.RotateSecret(s) },
		RotateEncryptionKey: util.RotateEncryptionKey,
		RotateRabbitMQURL: func(newURL string) {
			if err := tailer.RotateRabbitMQURL(newURL); err != nil {
				slog.Error("rabbitmq: failed to apply rotated URL", "error", err)
			}
		},
		// PostgreSQL: the live connection pool is wrapped behind a DynamicPool
		// so rotated credentials can be applied atomically by swapping the
		// underlying *sql.DB.
		RotatePostgreSQLDSN: func(newDSN string) {
			if err := dynamicPool.Rotate(newDSN); err != nil {
				slog.Error("postgres: failed to rotate connection pool", "error", err)
				return
			}
			slog.Info("postgres: swapped to rotated connection pool")
		},
	})

	engine := parser.NewEngine(dynamicPool)
	ic := control.New(ctx, sharedClient)

	// With Redis configured, cache invalidation and the slow-query log get
	// shared across replicas instead of staying local to whichever replica
	// handled the triggering request. The tailer is gated on the keepalived
	// VIP marker file (see tailer.go vipMarkerPath) instead of a Redis
	// leader elector - only the API on the VIP-holding node tails the log
	// file, guaranteeing co-location with rsyslog and eliminating NFS
	// read-cache delay. All of this is a no-op when sharedClient is nil.
	if sharedClient != nil {
		broadcaster := sharedstate.NewBroadcaster(sharedClient)
		handler.SetCacheBroadcaster(broadcaster)
		go handler.StartCacheInvalidationSubscriber(ctx, broadcaster)
		handler.SetSlowQueryStore(sharedClient)
	}

	logFilePath := os.Getenv("LOG_FILE_PATH")
	if logFilePath == "" {
		logFilePath = "/data/logs.jsonl"
	}
	alertEngine := alertengine.NewEngine(ctx, dynamicPool, sharedClient)
	audit.SetAlertEngine(alertEngine)
	notifHub := notifyhub.NewHub(ctx, sharedClient)
	alertEngine.SetOnInApp(notifHub.Publish)

	// Redis-backed (shared across replicas) when sharedClient is set, so the
	// dashboard's logs/sec figure is the same regardless of which replica
	// answers the request - falls back to in-memory otherwise, same as
	// everything else gated on sharedClient above.
	logRate := sharedstate.NewRateCounter(sharedClient, "lograte")
	handler.SetLogRateCounter(logRate)

	// Device silence checks run independently on every replica: the read
	// (mv_device_stats) is cheap and the per-rule-per-device cooldown key in
	// alertEngine's counter store (Redis-backed when configured) already
	// dedupes duplicate fires, the same way vacuum/MV refresh above already
	// run redundantly on every replica without an elector.
	silenceCheckMin := 5
	if v, err := strconv.Atoi(db.GetSetting(dynamicPool.Get(), "device_silence_check_minutes", "5")); err == nil && v > 0 {
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
				alertEngine.CheckDeviceSilence()
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
				if err := handler.SyncRelayConfig(dynamicPool); err != nil {
					slog.Warn("relay config sync failed during periodic check", "error", err)
				}
				alertEngine.CheckRelayCertExpiring()
			}
		}
	}()

r := gin.New()
	configureTrustedProxies(r)
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.ErrorHandler())
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.ServerIdentity())
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

	r.GET("/api/health", handler.HealthCheck(dynamicPool))
	r.GET("/api/version", versionHandler)
	r.GET("/api/settings/default-language", defaultLanguageHandler(dynamicPool))

	metricsGroup := r.Group("/api")
	metricsGroup.Use(authCfg.JWTRequired())
	metricsGroup.GET("/metrics", handler.PrometheusMetrics(dynamicPool))

	r.POST("/api/auth/login", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), rateLimitMiddleware(loginLimiter), handler.Login(dynamicPool, authCfg))
	r.POST("/api/auth/refresh", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), rateLimitMiddleware(refreshLimiter), handler.RefreshDeviceID(), handler.Refresh(dynamicPool, authCfg))
	r.POST("/api/auth/logout", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.Logout(dynamicPool))
	r.GET("/api/status/initialized", handler.CheckInitialized(dynamicPool))
	r.POST("/api/init", middleware.RequireJSON(), middleware.MaxRequestBodySize(8*1024), rateLimitMiddleware(initLimiter), handler.Initialize(dynamicPool))
	r.GET("/api/init/generate-keys", handler.GenerateKeys())
	r.GET("/api/init/db-config", handler.GetDbConfig(dynamicPool))

	authGroup := r.Group("/api")
	authGroup.Use(authCfg.JWTRequired())
	authGroup.Use(handler.RefreshDeviceID())
	authGroup.Use(handler.CSRFRequired())
	authGroup.Use(middleware.UpdateSessionActivity(dynamicPool))
	{
		authGroup.POST("/logs", handler.GetLogs(dynamicPool))
		authGroup.POST("/logs/count", handler.GetLogsCount(dynamicPool))

		authGroup.POST("/stats/dashboard", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.GetDashboardStats(dynamicPool))
		authGroup.POST("/stats/devices", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.GetDeviceStats(dynamicPool))
		authGroup.POST("/stats/severity", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.GetSeverityStats(dynamicPool))
		authGroup.POST("/stats/timeline", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.GetTimelineStats(dynamicPool))
		authGroup.GET("/stats/rate", handler.GetLogsRate())
		authGroup.GET("/devices", handler.GetDevices(dynamicPool))
		authGroup.POST("/export/csv", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.ExportCSV(dynamicPool))
		authGroup.POST("/export/html", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.ExportHTML(dynamicPool))
		authGroup.GET("/auth/me", handler.GetMe(dynamicPool))
		authGroup.POST("/auth/change-password", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), rateLimitMiddleware(changePasswordLimiter), handler.ChangePassword(dynamicPool))
		authGroup.GET("/auth/sessions", handler.ListSessions(dynamicPool))
		authGroup.DELETE("/auth/sessions/:id", handler.RevokeSession(dynamicPool))
		authGroup.GET("/auth/session-check", handler.CheckSession(dynamicPool))
		authGroup.POST("/auth/activity", handler.Activity(dynamicPool))

		notificationsGate := handler.RequireNotificationsEnabled(dynamicPool)

		// Deliberately ungated: the bell needs GET /notifications to reach it
		// even while disabled, since that's how it learns enabled:false and
		// hides itself. mark-read is harmless either way.
		authGroup.GET("/notifications", handler.GetNotifications(dynamicPool))
		authGroup.POST("/notifications/mark-read", handler.MarkNotificationsRead(dynamicPool))
		authGroup.GET("/notifications/stream", notificationsGate, handler.StreamNotifications(notifHub, dynamicPool))

		authGroup.GET("/push/vapid-public-key", notificationsGate, handler.GetVAPIDPublicKey(dynamicPool))
		authGroup.POST("/push/subscribe", notificationsGate, handler.SubscribePush(dynamicPool))
		authGroup.POST("/push/unsubscribe", notificationsGate, handler.UnsubscribePush(dynamicPool))

		// Readable by every authenticated role (including viewer) - channel
		// secrets are never included in the response, only config + a
		// has_secret flag, so there's nothing sensitive to gate here. Creating,
		// editing and deleting channels stays admin-only (adminGroup below).
		authGroup.GET("/admin/notification-channels", notificationsGate, handler.ListNotificationChannels(dynamicPool))

		// Readable by every authenticated role (including viewer) - db.GetAllAlerts
		// and db.GetNotificationHistory already drop admin-only rule types for
		// non-admins, unless the caller is specifically targeted via a channel's
		// user_ids (same override used at notification-delivery time in
		// notify/push.go and handler.StreamNotifications). Gating the route to
		// admin/editor only would make that per-row filtering unreachable for
		// viewers and hide non-admin-only rules/history from them too. Creating,
		// updating and deleting alert rules stays editor/admin-only (editorGroup
		// below).
		authGroup.GET("/alerts", notificationsGate, handler.ListAlerts(dynamicPool))
		authGroup.POST("/admin/notifications/history", notificationsGate, middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.GetNotificationHistory(dynamicPool))

		authGroup.GET("/parsers", handler.ListParsers(engine))
		authGroup.POST("/parsers/fields", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.ListParsedFields(engine))
		authGroup.GET("/dashboards", handler.ListDashboards(dynamicPool))
		authGroup.GET("/dashboards/:id", handler.GetDashboard(dynamicPool))
		authGroup.POST("/dashboards/:id/data", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.GetDashboardData(dynamicPool))
		authGroup.POST("/dashboards/:id/count", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.GetDashboardDataCount(dynamicPool))
		authGroup.POST("/dashboards/:id/export/csv", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.ExportDashboardCSV(dynamicPool))
		authGroup.POST("/dashboards/:id/export/html", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.ExportDashboardHTML(dynamicPool))
		authGroup.PATCH("/dashboards/:id/pin", handler.TogglePinDashboard(dynamicPool))

		editorGroup := authGroup.Group("")
		editorGroup.Use(authCfg.RoleRequired("admin", "editor"))
		{
			editorGroup.POST("/parsers", handler.CreateParser(engine))
			editorGroup.PUT("/parsers/:id", handler.UpdateParser(engine))
			editorGroup.DELETE("/parsers/:id", handler.DeleteParser(engine))
			editorGroup.POST("/parsers/:id/clone", handler.CloneParser(engine))
			editorGroup.POST("/parsers/test", handler.TestParser(engine))
			editorGroup.POST("/parsers/reparse", handler.ReparseUnparsed(engine))

			editorGroup.POST("/dashboards", handler.CreateDashboard(dynamicPool))
			editorGroup.PUT("/dashboards/:id", handler.UpdateDashboard(dynamicPool))
			editorGroup.DELETE("/dashboards/:id", handler.DeleteDashboard(dynamicPool))
			editorGroup.PATCH("/dashboards/:id/public", handler.TogglePublicDashboard(dynamicPool))

			editorGroup.POST("/alerts", notificationsGate, handler.CreateAlert(dynamicPool, alertEngine))
			editorGroup.PUT("/alerts/:id", notificationsGate, handler.UpdateAlert(dynamicPool, alertEngine))
			editorGroup.DELETE("/alerts/:id", notificationsGate, handler.DeleteAlert(dynamicPool, alertEngine))

			editorGroup.GET("/users/directory", handler.ListUserDirectory(dynamicPool))
		}

		adminGroup := authGroup.Group("/admin")
		adminGroup.Use(authCfg.AdminRequired())
		{
			adminGroup.GET("/users", handler.ListUsers(dynamicPool))
			adminGroup.POST("/users", handler.CreateUser(dynamicPool))
			adminGroup.PUT("/users/:id", handler.UpdateUser(dynamicPool))
			adminGroup.DELETE("/users/:id", handler.DeleteUser(dynamicPool))
			adminGroup.PUT("/users/:id/reset-password", handler.ResetPassword(dynamicPool))
			adminGroup.POST("/users/:id/unlock", handler.UnlockUserHandler(dynamicPool))
			adminGroup.GET("/settings", handler.GetSettings(dynamicPool))
			adminGroup.PUT("/settings", handler.UpdateSettings(dynamicPool))
			adminGroup.POST("/settings/cleanup", handler.CleanupLogs(dynamicPool))
			adminGroup.DELETE("/logs", handler.PurgeAllLogs(dynamicPool, ic))
			adminGroup.POST("/ingestion/pause", handler.PauseIngestion(ic))
			adminGroup.POST("/ingestion/resume", handler.ResumeIngestion(ic))
			adminGroup.GET("/ingestion/status", handler.GetIngestionStatus(ic))
			adminGroup.GET("/tailer-metrics", handler.GetTailerMetrics())
			adminGroup.POST("/ldap/test", handler.TestLDAP(dynamicPool))
			adminGroup.POST("/audit-log", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.GetAuditLog(dynamicPool))
			adminGroup.POST("/audit-logs", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.GetAuditLogsHandler(dynamicPool))
			adminGroup.GET("/slow-queries", handler.GetSlowQueries())
			adminGroup.DELETE("/slow-queries", handler.ClearSlowQueriesHandler())
			adminGroup.GET("/health/containers", handler.GetContainersHealth(dynamicPool))
			adminGroup.PUT("/devices/:ip/alias", handler.UpdateDeviceAlias(dynamicPool))
		adminGroup.POST("/ssl/upload", handler.UploadSSLCerts(dynamicPool))
		adminGroup.POST("/nginx-reload", handler.ReloadNginx(dynamicPool))
		adminGroup.GET("/rabbitmq-url", handler.GetRabbitMQURL())

			adminGroup.GET("/relay/whitelist", handler.ListRelayWhitelist(dynamicPool))
			adminGroup.POST("/relay/whitelist", handler.CreateRelayWhitelistEntry(dynamicPool))
			adminGroup.DELETE("/relay/whitelist/:id", handler.DeleteRelayWhitelistEntry(dynamicPool))
			adminGroup.POST("/relay/whitelist/:id/certificate", handler.GenerateCertificateForWhitelistEntry(dynamicPool))
			adminGroup.GET("/relay/certificates", handler.ListRelayCertificates(dynamicPool))
			adminGroup.POST("/relay/certificates", handler.CreateRelayCertificate(dynamicPool))
			adminGroup.DELETE("/relay/certificates/:id", handler.RevokeRelayCertificate(dynamicPool))
			adminGroup.POST("/relay/certificates/:id/regenerate", handler.RegenerateRelayCertificate(dynamicPool))

			adminGroup.DELETE("/notifications/history", notificationsGate, handler.ClearNotificationHistory(dynamicPool))

			// API Key management
			adminGroup.GET("/api-keys", handler.ListAPIKeys(dynamicPool))
			adminGroup.POST("/api-keys", handler.CreateAPIKey(dynamicPool))
			adminGroup.PUT("/api-keys/:id", handler.UpdateAPIKey(dynamicPool))
			adminGroup.DELETE("/api-keys/:id", handler.DeleteAPIKey(dynamicPool))
			adminGroup.POST("/api-keys/:id/reset", handler.ResetAPIKey(dynamicPool))
		}

		// Same /admin path prefix as adminGroup above, but readable/usable by
		// editors too - they can already create alert rules (editorGroup), so
		// they need to see whether their rules actually fired, and to create
		// their own notification channels to assign to those rules.
		// Create/update/delete are further restricted to the channel's own
		// creator at the handler level (see handler.channelOwnedByCaller) -
		// an editor (or admin) can only ever modify a channel they made
		// themselves, or one predating the created_by column entirely.
		// (Notification history itself now lives on authGroup above - see
		// comment there - since viewers need read access too.)
		adminEditorGroup := authGroup.Group("/admin")
		adminEditorGroup.Use(authCfg.RoleRequired("admin", "editor"))
		{
			adminEditorGroup.POST("/notification-channels", notificationsGate, handler.CreateNotificationChannel(dynamicPool))
			adminEditorGroup.PUT("/notification-channels/:id", notificationsGate, handler.UpdateNotificationChannel(dynamicPool))
			adminEditorGroup.DELETE("/notification-channels/:id", notificationsGate, handler.DeleteNotificationChannel(dynamicPool))
			adminEditorGroup.POST("/notification-channels/:id/test", notificationsGate, handler.TestNotificationChannel(dynamicPool, notifHub))
		}
	}

	// Public API routes (API key authentication)
	publicAPI := r.Group("/api/v1")
	publicAPI.Use(middleware.APIKeyAuth(dynamicPool))
	{
		publicAPI.POST("/logs/export", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.ExportJSON(dynamicPool))
		publicAPI.POST("/logs/export-parsed", middleware.RequireJSON(), middleware.MaxRequestBodySize(4*1024), handler.ExportParsedJSON(dynamicPool))
		publicAPI.GET("/stats", handler.ExportStats(dynamicPool))
	}

	// The real listener only needs the schema (ready since <-schemaReady
	// above) and routes/auth (registered just above) - it does not need to
	// wait on wg below, which exists to synchronize the tailer's
	// leader-election race across replicas (see its comment) and gates the
	// nginx/relay config sync goroutines, none of which the HTTP API itself
	// depends on. Binding here rather than after wg.Wait() is what actually
	// delivers on RefreshMaterializedViews's own comment above ("must not
	// gate the HTTP listener") - mv_device_stats alone has been observed
	// taking 10+ minutes on a large syslog_logs, and wg.Wait() was blocking
	// exactly this ListenAndServe call on it.
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	{
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := startupSrv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("startup health server did not shut down cleanly", "error", err)
		}
		cancel()
	}

	go func() {
		db.SetAppStarting(false)
		slog.Info("server starting", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for all startup tasks (migration + MV refresh, nginx sync, relay
	// sync) to finish before registering this replica and starting the
	// tailer - this ensures every replica reaches the same point in its
	// startup sequence before the leader-election race begins. Deliberately
	// after the real listener is already up (see above); the tailer's own
	// leader election, not the API, is what needs this synchronization.
	wg.Wait()

	go func() {
		identity := os.Getenv("SWARM_TASK_IDENTITY")
		if identity == "" {
			if h, err := os.Hostname(); err == nil {
				identity = h
			}
		}
		sharedstate.WaitForReplicas(ctx, sharedClient, identity, apiReplicas, 10*time.Minute)
		tailer.Run(ctx, dynamicPool, logFilePath, engine, ic, alertEngine, logRate, handler.ReopenRsyslogLogFile, sharedClient)
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down server...")
	maintCancel()
	stopVacuum()
	stopMV()
	stopTokenCleanup()
	stopArchiveCleanup()
	stopPartitions()
	stopIfPersistent(loginLimiter)
	stopIfPersistent(changePasswordLimiter)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("forced shutdown", "error", err)
	}
	slog.Info("server stopped")
}

// startupHealthHandler serves only /api/health - just enough for Docker's
// HEALTHCHECK (and a Swarm deployment's health-based task monitor) to see
// the container as "starting" rather than unreachable while the schema
// migration is still running. See the startupSrv comment in main().
func startupHealthHandler(pool *db.DynamicPool) http.Handler {
	r := gin.New()
	configureTrustedProxies(r)
	r.Use(gin.Recovery())
	r.GET("/api/health", handler.HealthCheck(pool))
	return r
}

// waitForWizardDatabase runs a minimal, database-less HTTP server exposing
// only the setup wizard's endpoints, and blocks until the wizard submits
// working database settings. It returns the resulting live pool so
// main() can continue its normal startup sequence on it.
func waitForWizardDatabase(port string, sharedClient *sharedstate.Client) *db.DynamicPool {
	ready := make(chan *db.DynamicPool, 1)

	r := gin.New()
	configureTrustedProxies(r)
	r.Use(gin.Recovery())
	r.Use(middleware.ErrorHandler())
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.ServerIdentity())
	r.Use(middleware.GzipCompress())

	initLimiter := newLimiter(sharedClient, "wizard-init", 3, time.Hour, "")
	testDbLimiter := newLimiter(sharedClient, "wizard-test-db", 20, 10*time.Minute, "")
	r.GET("/api/health", handler.HealthCheckStandalone())
	r.GET("/api/version", versionHandler)
	r.GET("/api/settings/default-language", defaultLanguageHandler(nil))
	r.GET("/api/status/initialized", handler.CheckInitializedStandalone())
	r.GET("/api/init/generate-keys", handler.GenerateKeys())
	r.GET("/api/init/db-config", handler.GetDbConfig(nil))
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

	dynamicPool := <-ready
	slog.Info("database settings received from setup wizard, handing off to the main server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("setup wizard server did not shut down cleanly", "error", err)
	}

	return dynamicPool
}

// defaultTrustedProxies covers the private/loopback ranges a reverse proxy
// (this app's own nginx frontend, plus any Docker bridge/overlay network)
// realistically sits in. It intentionally does NOT include public ranges, so
// a client reaching nginx from the internet cannot spoof its source IP via
// X-Forwarded-For: nginx appends the real (public) peer address, which falls
// outside these ranges and is therefore what c.ClientIP() returns.
var defaultTrustedProxies = []string{
	"127.0.0.1/8", "::1/128",
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7",
}

// configureTrustedProxies replaces Gin's insecure default (trust every proxy,
// which makes X-Forwarded-For fully client-controlled) with an explicit list.
// Override with TRUSTED_PROXIES (comma-separated CIDRs or IPs) for deployments
// whose proxy sits in a different range. Empty/"none" disables proxy trust
// entirely, so the direct TCP peer is always used as the client IP.
func configureTrustedProxies(r *gin.Engine) {
	proxies := defaultTrustedProxies
	if v := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES")); v != "" {
		if strings.EqualFold(v, "none") {
			proxies = nil
		} else {
			proxies = nil
			for _, p := range strings.Split(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					proxies = append(proxies, p)
				}
			}
		}
	}
	if err := r.SetTrustedProxies(proxies); err != nil {
		slog.Error("failed to set trusted proxies; falling back to trusting none", "error", err)
		_ = r.SetTrustedProxies(nil)
	}
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
