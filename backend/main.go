package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"syslog-gui/auth"
	"syslog-gui/control"
	"syslog-gui/db"
	"syslog-gui/handler"
	"syslog-gui/middleware"
	"syslog-gui/parser"
	"syslog-gui/tailer"

	"github.com/gin-gonic/gin"
)

type rateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	limit    int
	window   time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		requests: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	times := rl.requests[ip]
	valid := make([]time.Time, 0, len(times))
	for _, t := range times {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= rl.limit {
		rl.requests[ip] = valid
		return false
	}

	rl.requests[ip] = append(valid, now)
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
		database = waitForWizardDatabase(port)
	}
	defer database.Close()

	db.SetAppStarting(true)
	migrationDone := make(chan struct{})
	go func() {
		defer close(migrationDone)
		if err := db.Migrate(database); err != nil {
			slog.Error("failed to migrate database", "error", err)
			os.Exit(1)
		}
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

	ctx, maintCancel := context.WithCancel(context.Background())
	stopVacuum, stopMV, stopTokenCleanup := db.StartMaintenance(ctx, database)

	// Fast MV refresh for dashboard_summary (every 30s) to keep stats responsive.
	// RefreshMV itself skips while migration is in progress, so it's safe to
	// start this ticker immediately.
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
				db.RefreshMV(database)
			}
		}
	}()

	// auth.Init/parser.NewEngine/the tailer all query tables that only exist
	// once db.Migrate has run (users, parsers, syslog_logs) - on a brand new
	// database these queries can otherwise race ahead of table creation and
	// fail. Everything below this point already ran sequentially after
	// auth.Init anyway, so waiting here doesn't add any new startup latency.
	<-migrationDone

	if err := auth.Init(database); err != nil {
		slog.Error("auth initialization failed", "error", err)
		os.Exit(1)
	}

	engine := parser.NewEngine(database)
	ic := control.NewIngestionController()

	logFilePath := os.Getenv("LOG_FILE_PATH")
	if logFilePath == "" {
		logFilePath = "/data/logs.jsonl"
	}
	go tailer.Start(database, logFilePath, engine, ic)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.ErrorHandler())
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.GzipCompress())
	r.Use(middleware.ETag())
	// CORS is handled entirely by the frontend's nginx reverse proxy (see
	// frontend/nginx.conf and handler.reloadNginx) - clients only ever reach
	// this API through it, so there's no CORS handling here.

	loginLimiter := newRateLimiter(5, time.Minute)
	refreshLimiter := newRateLimiter(10, time.Minute)
	initLimiter := newRateLimiter(3, time.Hour)

	r.GET("/api/health", handler.HealthCheck(database))
	r.POST("/api/auth/login", rateLimitMiddleware(loginLimiter), handler.Login(database))
	r.POST("/api/auth/refresh", rateLimitMiddleware(refreshLimiter), handler.Refresh(database))
	r.POST("/api/auth/logout", handler.Logout(database))
	r.GET("/api/status/initialized", handler.CheckInitialized(database))
	r.POST("/api/init", rateLimitMiddleware(initLimiter), handler.Initialize(database))
	r.GET("/api/init/generate-keys", handler.GenerateKeys())
	r.GET("/api/init/db-config", handler.GetDbConfig())

	authGroup := r.Group("/api")
	authGroup.Use(auth.JWTRequired())
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
		changePasswordLimiter := newRateLimiter(5, time.Minute)
		authGroup.POST("/auth/change-password", rateLimitMiddleware(changePasswordLimiter), handler.ChangePassword(database))

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
		}

		adminGroup := authGroup.Group("/admin")
		adminGroup.Use(auth.AdminRequired())
		{
			adminGroup.GET("/users", handler.ListUsers(database))
			adminGroup.POST("/users", handler.CreateUser(database))
			adminGroup.PUT("/users/:id", handler.UpdateUser(database))
			adminGroup.DELETE("/users/:id", handler.DeleteUser(database))
			adminGroup.PUT("/users/:id/reset-password", handler.ResetPassword(database))
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
			adminGroup.PUT("/devices/:ip/alias", handler.UpdateDeviceAlias(database))
			adminGroup.POST("/ssl/upload", handler.UploadSSLCerts(database))
			adminGroup.POST("/nginx-reload", handler.ReloadNginx(database))
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
func waitForWizardDatabase(port string) *sql.DB {
	ready := make(chan *sql.DB, 1)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.ErrorHandler())
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.GzipCompress())

	initLimiter := newRateLimiter(3, time.Hour)
	testDbLimiter := newRateLimiter(20, 10*time.Minute)
	r.GET("/api/health", handler.HealthCheckStandalone())
	r.GET("/api/status/initialized", handler.CheckInitializedStandalone())
	r.GET("/api/init/generate-keys", handler.GenerateKeys())
	r.GET("/api/init/db-config", handler.GetDbConfig())
	r.POST("/api/init/test-db", rateLimitMiddleware(testDbLimiter), handler.TestDatabaseConfig())
	r.POST("/api/init", rateLimitMiddleware(initLimiter), handler.InitializeStandalone(ready))

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


func rateLimitMiddleware(rl *rateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if !rl.allow(ip) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many login attempts, try again later"})
			c.Abort()
			return
		}
		c.Next()
	}
}
