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

	// Initialize parser engine
	engine := parser.NewEngine(database)

	// Start file tailer for rsyslog JSON lines
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
	r.POST("/api/auth/register", handler.Register(database))
	r.POST("/api/ingest/batch", handler.IngestBatch(database))

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

		authGroup.GET("/parsers", handler.ListParsers(engine))
		authGroup.POST("/parsers", handler.CreateParser(engine))
		authGroup.PUT("/parsers/:id", handler.UpdateParser(engine))
		authGroup.DELETE("/parsers/:id", handler.DeleteParser(engine))
		authGroup.POST("/parsers/test", handler.TestParser(engine))
		authGroup.POST("/parsers/reparse", handler.ReparseUnparsed(engine))
		authGroup.GET("/parsers/fields", handler.ListParsedFields(engine))

		authGroup.GET("/dashboards", handler.ListDashboards(database))
		authGroup.POST("/dashboards", handler.CreateDashboard(database))
		authGroup.GET("/dashboards/:id", handler.GetDashboard(database))
		authGroup.PUT("/dashboards/:id", handler.UpdateDashboard(database))
		authGroup.DELETE("/dashboards/:id", handler.DeleteDashboard(database))
		authGroup.GET("/dashboards/:id/data", handler.GetDashboardData(database))
		authGroup.PATCH("/dashboards/:id/pin", handler.TogglePinDashboard(database))
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