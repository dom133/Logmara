package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
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

	if err := validateEnv(); err != nil {
		slog.Error("environment validation failed", "error", err)
		os.Exit(1)
	}

	dsn := os.Getenv("DATABASE_URL")

	database, err := db.Connect(dsn)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	db.SetAppStarting(true)
	go func() {
		if err := db.Migrate(database); err != nil {
			slog.Error("failed to migrate database", "error", err)
			os.Exit(1)
		}
		db.RefreshMaterializedViews(database)
		db.SetAppStarting(false)
		slog.Info("database migration and initialization complete")
	}()

	ctx, maintCancel := context.WithCancel(context.Background())
	stopVacuum, stopMV := db.StartMaintenance(ctx, database)

	auth.Init(database)

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
	r.Use(corsMiddleware(database))

	loginLimiter := newRateLimiter(5, time.Minute)
	refreshLimiter := newRateLimiter(10, time.Minute)
	initLimiter := newRateLimiter(3, time.Hour)

	r.GET("/api/health", handler.HealthCheck(database))
	r.POST("/api/auth/login", rateLimitMiddleware(loginLimiter), handler.Login(database))
	r.POST("/api/auth/refresh", rateLimitMiddleware(refreshLimiter), handler.Refresh(database))
	r.POST("/api/auth/logout", handler.Logout(database))
	r.POST("/api/ingest/batch", handler.IngestBatch(database, engine, ic))
	r.GET("/api/status/initialized", handler.CheckInitialized(database))
	r.POST("/api/init", rateLimitMiddleware(initLimiter), handler.Initialize(database))
	r.GET("/api/init/generate-keys", handler.GenerateKeys())
	r.GET("/api/init/db-config", handler.GetDbConfig())

	authGroup := r.Group("/api")
	authGroup.Use(auth.JWTRequired())
	{
		authGroup.GET("/logs", handler.GetLogs(database))
		
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
		}
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("forced shutdown", "error", err)
	}
	slog.Info("server stopped")
}

func validateEnv() error {
	required := []string{"DATABASE_URL"}
	for _, env := range required {
		if os.Getenv(env) == "" {
			return fmt.Errorf("%s environment variable is required", env)
		}
	}
	return nil
}

func corsMiddleware(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		origins := db.GetSetting(database, "cors_origins", "")
		if origins == "" {
			host := c.Request.Host
			c.Writer.Header().Set("Access-Control-Allow-Origin", "http://"+host)
		} else {
			origin := c.GetHeader("Origin")
			for _, allowed := range strings.Split(origins, ",") {
				allowed = strings.TrimSpace(allowed)
				if allowed == origin || allowed == "*" {
					c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
					break
				}
			}
		}
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
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
