package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"syslog-gui/auth"
	"syslog-gui/db"
	"syslog-gui/handler"
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
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	database, err := db.Connect(dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}

	auth.Init(database)

	engine := parser.NewEngine(database)

	logFilePath := os.Getenv("LOG_FILE_PATH")
	if logFilePath == "" {
		logFilePath = "/data/logs.jsonl"
	}
	go tailer.Start(database, logFilePath, engine)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware(database))

	loginLimiter := newRateLimiter(5, time.Minute)

	r.POST("/api/auth/login", rateLimitMiddleware(loginLimiter), handler.Login(database))
	r.POST("/api/auth/refresh", handler.Refresh(database))
	r.POST("/api/auth/logout", handler.Logout(database))
	r.POST("/api/ingest/batch", handler.IngestBatch(database, engine))
	r.GET("/api/status/initialized", handler.CheckInitialized(database))
	r.POST("/api/init", handler.Initialize(database))
	r.GET("/api/init/generate-keys", handler.GenerateKeys())
	r.GET("/api/init/db-config", handler.GetDbConfig())

authGroup := r.Group("/api")
  authGroup.Use(auth.JWTRequired())
  {
    authGroup.GET("/logs", handler.GetLogs(database))
    authGroup.GET("/logs/stream", handler.StreamLogs(database))
    authGroup.GET("/stats/dashboard", handler.GetDashboardStats(database))
    authGroup.GET("/stats/devices", handler.GetDeviceStats(database))
    authGroup.GET("/stats/severity", handler.GetSeverityStats(database))
    authGroup.GET("/stats/timeline", handler.GetTimelineStats(database))
    authGroup.GET("/devices", handler.GetDevices(database))
    authGroup.GET("/export/csv", handler.ExportCSV(database))
    authGroup.GET("/export/html", handler.ExportHTML(database))
    authGroup.GET("/auth/me", handler.GetMe())
    authGroup.POST("/auth/change-password", handler.ChangePassword(database))

    // Read-only endpoints accessible to all authenticated users (including viewer)
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
      adminGroup.DELETE("/logs", handler.PurgeAllLogs(database))
      adminGroup.POST("/ldap/test", handler.TestLDAP(database))
      adminGroup.GET("/audit-log", handler.GetAuditLog(database))
      adminGroup.PUT("/devices/:ip/alias", handler.UpdateDeviceAlias(database))
    }
  }

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server starting on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
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