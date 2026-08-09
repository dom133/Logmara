package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"logmara/util"
)

var appStartTime = time.Now()

// PrometheusMetrics exposes /metrics in Prometheus text exposition format.
// It includes app uptime, goroutines, DB pool stats, and slow query count.
func PrometheusMetrics(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		stats := database.Stats()
		up := time.Since(appStartTime).Seconds()

		var slowQueries int
		database.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action = 'slow_query'").Scan(&slowQueries)

		var ingestPaused bool
		database.QueryRow("SELECT COALESCE(value, 'false')::boolean FROM app_settings WHERE key = 'ingestion_paused'").Scan(&ingestPaused)
		ingestPausedVal := 0
		if ingestPaused {
			ingestPausedVal = 1
		}

		lines := []string{
			"# HELP app_up Whether the application is running.",
			"# TYPE app_up gauge",
			"app_up 1",
			"",
			"# HELP app_uptime_seconds Seconds since the application started.",
			"# TYPE app_uptime_seconds gauge",
			fmt.Sprintf("app_uptime_seconds %.1f", up),
			"",
			"# HELP go_goroutines Number of goroutines currently running.",
			"# TYPE go_goroutines gauge",
			fmt.Sprintf("go_goroutines %d", runtime.NumGoroutine()),
			"",
			"# HELP db_pool_open_connections Open connections to the database.",
			"# TYPE db_pool_open_connections gauge",
			fmt.Sprintf("db_pool_open_connections %d", stats.OpenConnections),
			"",
			"# HELP db_pool_in_use_connections Connections currently in use.",
			"# TYPE db_pool_in_use_connections gauge",
			fmt.Sprintf("db_pool_in_use_connections %d", stats.InUse),
			"",
			"# HELP db_pool_idle_connections Idle connections to the database.",
			"# TYPE db_pool_idle_connections gauge",
			fmt.Sprintf("db_pool_idle_connections %d", stats.Idle),
			"",
			"# HELP db_pool_max_open_connections Max open connections allowed.",
			"# TYPE db_pool_max_open_connections gauge",
			fmt.Sprintf("db_pool_max_open_connections %d", stats.MaxOpenConnections),
			"",
			"# HELP db_pool_wait_count Total number of connections waited for.",
			"# TYPE db_pool_wait_count counter",
			fmt.Sprintf("db_pool_wait_count_total %d", stats.WaitCount),
			"",
			"# HELP db_pool_wait_duration_ns Total time blocked waiting for a connection.",
			"# TYPE db_pool_wait_duration_ns counter",
			fmt.Sprintf("db_pool_wait_duration_nano_total %d", stats.WaitDuration.Nanoseconds()),
			"",
			"# HELP db_pool_max_idle_closed Total number of connections closed due to idle timeout.",
			"# TYPE db_pool_max_idle_closed counter",
			fmt.Sprintf("db_pool_max_idle_closed_total %d", stats.MaxIdleClosed),
			"",
			"# HELP db_pool_max_lifetime_closed Total number of connections closed due to max lifetime.",
			"# TYPE db_pool_max_lifetime_closed counter",
			fmt.Sprintf("db_pool_max_lifetime_closed_total %d", stats.MaxLifetimeClosed),
			"",
			"# HELP ingest_paused Whether log ingestion is paused.",
			"# TYPE ingest_paused gauge",
			fmt.Sprintf("ingest_paused %d", ingestPausedVal),
			"",
			"# HELP slow_queries_total Total number of slow queries recorded.",
			"# TYPE slow_queries_total gauge",
			fmt.Sprintf("slow_queries_total %d", slowQueries),
			"",
			"# HELP secrets_loaded_total Total number of secret loads since startup.",
			"# TYPE secrets_loaded_total counter",
			fmt.Sprintf("secrets_loaded_total %d", util.GetSecretLoadCount()),
		}

		c.String(http.StatusOK, strings.Join(lines, "\n")+"\n")
	}
}