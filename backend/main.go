package main

import (
	"log"
	"net/http"
	"os"

	"syslog-gui/auth"
	"syslog-gui/db"
	"syslog-gui/handler"
	"syslog-gui/parser"
	"syslog-gui/tailer"

	"github.com/gin-gonic/gin"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://syslog:syslogpass@postgres:5432/syslog_db?sslmode=disable"
	}

	database, err := db.Connect(dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}

	auth.InitAdmin(database)

	engine := parser.NewEngine(database)

	logFilePath := os.Getenv("LOG_FILE_PATH")
	if logFilePath == "" {
		logFilePath = "/data/logs.jsonl"
	}
	go tailer.Start(database, logFilePath, engine)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "*")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "*")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	r.POST("/api/auth/login", handler.Login(database))
	r.POST("/api/ingest/batch", handler.IngestBatch(database))
	r.GET("/api/status/initialized", handler.CheckInitialized(database))
	r.POST("/api/init", handler.Initialize(database))
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
		authGroup.GET("/auth/me", handler.GetMe())
		authGroup.POST("/auth/change-password", handler.ChangePassword(database))

		editorGroup := authGroup.Group("")
		editorGroup.Use(auth.RoleRequired("admin", "editor"))
		{
			editorGroup.GET("/parsers", handler.ListParsers(engine))
			editorGroup.POST("/parsers", handler.CreateParser(engine))
			editorGroup.PUT("/parsers/:id", handler.UpdateParser(engine))
			editorGroup.DELETE("/parsers/:id", handler.DeleteParser(engine))
			editorGroup.POST("/parsers/test", handler.TestParser(engine))
			editorGroup.POST("/parsers/reparse", handler.ReparseUnparsed(engine))
			editorGroup.GET("/parsers/fields", handler.ListParsedFields(engine))

			editorGroup.GET("/dashboards", handler.ListDashboards(database))
			editorGroup.POST("/dashboards", handler.CreateDashboard(database))
			editorGroup.GET("/dashboards/:id", handler.GetDashboard(database))
			editorGroup.PUT("/dashboards/:id", handler.UpdateDashboard(database))
			editorGroup.DELETE("/dashboards/:id", handler.DeleteDashboard(database))
			editorGroup.GET("/dashboards/:id/data", handler.GetDashboardData(database))
			editorGroup.PATCH("/dashboards/:id/pin", handler.TogglePinDashboard(database))
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