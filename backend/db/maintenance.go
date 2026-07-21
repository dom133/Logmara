package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

func StartMaintenance(ctx context.Context, db *sql.DB) (func(), func(), func()) {
	vacuumInterval := getIntervalHours("VACUUM_INTERVAL_HOURS", "vacuum_interval_hours", 24)
	mvInterval := getIntervalMinutes("MV_REFRESH_INTERVAL_MIN", "mv_refresh_interval_min", 30)

	createPartitions(db)
	stopVacuum := startVacuumScheduler(ctx, db, vacuumInterval)
	stopMV := startMVScheduler(ctx, db, mvInterval)
	stopTokenCleanup := startRefreshTokenCleanupScheduler(ctx, db, 1*time.Hour)

	return stopVacuum, stopMV, stopTokenCleanup
}

func startVacuumScheduler(ctx context.Context, db *sql.DB, interval time.Duration) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		slog.Info("vacuum scheduler started", "interval_hours", interval.Hours())
		for {
			select {
			case <-ctx.Done():
				slog.Info("vacuum scheduler stopped")
				close(done)
				return
			case <-ticker.C:
				runVacuumAnalyze(db)
			}
		}
	}()
	return func() {
		<-done
	}
}

func startMVScheduler(ctx context.Context, db *sql.DB, interval time.Duration) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		slog.Info("mv refresh scheduler started", "interval_minutes", interval.Minutes())
		for {
			select {
			case <-ctx.Done():
				slog.Info("mv refresh scheduler stopped")
				close(done)
				return
			case <-ticker.C:
				if HasActiveSession(db) {
					RefreshMV(db)
				}
			}
		}
	}()
	return func() {
		<-done
	}
}

func startRefreshTokenCleanupScheduler(ctx context.Context, db *sql.DB, interval time.Duration) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		slog.Info("refresh token cleanup scheduler started", "interval_hours", interval.Hours())
		cleanupExpiredRefreshTokens(db)
		for {
			select {
			case <-ctx.Done():
				slog.Info("refresh token cleanup scheduler stopped")
				close(done)
				return
			case <-ticker.C:
				cleanupExpiredRefreshTokens(db)
			}
		}
	}()
	return func() {
		<-done
	}
}

// cleanupExpiredRefreshTokens prunes rows that can no longer be used to
// refresh a session: naturally expired tokens, and tokens already consumed
// by rotation (used=true) once they're well past the race-recovery grace
// window. Without this the refresh_tokens table grows forever, since every
// login and every refresh only ever inserts a new row.
func cleanupExpiredRefreshTokens(db *sql.DB) {
	res, err := db.Exec(
		"DELETE FROM refresh_tokens WHERE expires_at < NOW() OR (used = true AND used_at < NOW() - INTERVAL '1 day')",
	)
	if err != nil {
		slog.Error("refresh token cleanup failed", "err", err)
		return
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		slog.Info("refresh token cleanup completed", "rows_deleted", n)
	}
}

// runVacuumAnalyze targets only the currently-active partitions (this month
// and last month), since those are the only ones still receiving writes and
// therefore accumulating dead tuples/stale stats. Older partitions are
// static once the month rolls over - a daily VACUUM ANALYZE across the
// entire multi-month history is wasted I/O once the table is partitioned.
func runVacuumAnalyze(db *sql.DB) {
	partitions := activePartitionNames(db)
	if len(partitions) == 0 {
		slog.Info("running VACUUM ANALYZE", "table", "syslog_logs")
		if _, err := db.Exec("VACUUM ANALYZE syslog_logs"); err != nil {
			slog.Error("vacuum analyze failed", "err", err)
			return
		}
		slog.Info("VACUUM ANALYZE completed", "table", "syslog_logs")
		return
	}

	for _, name := range partitions {
		slog.Info("running VACUUM ANALYZE", "table", name)
		if _, err := db.Exec("VACUUM ANALYZE " + name); err != nil {
			slog.Error("vacuum analyze failed", "table", name, "err", err)
		}
	}
	if _, err := db.Exec("ANALYZE syslog_logs"); err != nil {
		slog.Error("analyze parent failed", "err", err)
	}
	slog.Info("VACUUM ANALYZE completed", "partitions", partitions)
}

func activePartitionNames(db *sql.DB) []string {
	var isPartitioned bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_class WHERE relname = 'syslog_logs' AND relkind = 'p')`).Scan(&isPartitioned); err != nil || !isPartitioned {
		return nil
	}

	now := time.Now().UTC()
	names := make([]string, 0, 2)
	for i := -1; i <= 0; i++ {
		t := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, i, 0)
		name := "syslog_logs_" + t.Format("2006_01")
		var exists bool
		if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_class WHERE relname = $1)", name).Scan(&exists); err == nil && exists {
			names = append(names, name)
		}
	}
	return names
}

// HasActiveSession reports whether any user currently holds a usable
// session - a refresh token that hasn't been consumed (by logout or by
// being rotated on refresh) and hasn't expired. Used to gate the periodic
// MV refresh scheduler: no point refreshing dashboard stats when nobody is
// logged in to look at them.
func HasActiveSession(db *sql.DB) bool {
	var active bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM refresh_tokens WHERE used = false AND expires_at > NOW())").Scan(&active)
	if err != nil {
		slog.Error("active session check failed", "err", err)
		return false
	}
	return active
}

func RefreshMV(db *sql.DB) {
	// Migrate() runs in its own goroutine and can take a while on a large
	// table (index builds, mv_device_stats aggregation) - the periodic
	// refresh tickers start immediately regardless, so without this guard
	// they can fire before Migrate() has created the views they're trying
	// to refresh.
	if IsAppStarting() {
		slog.Info("skipping materialized view refresh - migration still in progress")
		return
	}
	slog.Info("refreshing materialized views")
	for _, mv := range []string{"mv_dashboard_summary", "mv_dashboard_severity", "mv_timeline_hourly", "mv_device_stats", "mv_dashboard_top_errors"} {
		_, err := db.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY " + mv)
		if err != nil {
			slog.Error("mv refresh failed", "view", mv, "err", err)
		} else {
			slog.Info("materialized view refreshed", "view", mv)
		}
	}
}

func getIntervalHours(envKey, settingKey string, defaultHours int) time.Duration {
	if v := os.Getenv(envKey); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h > 0 {
			return time.Duration(h) * time.Hour
		}
	}
	return time.Duration(defaultHours) * time.Hour
}

func getIntervalMinutes(envKey, settingKey string, defaultMinutes int) time.Duration {
	if v := os.Getenv(envKey); v != "" {
		if m, err := strconv.Atoi(v); err == nil && m > 0 {
			return time.Duration(m) * time.Minute
		}
	}
	return time.Duration(defaultMinutes) * time.Minute
}

func createPartitions(db *sql.DB) {
	isPartitioned := false
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_class WHERE relname = 'syslog_logs' AND relkind = 'p')`).Scan(&isPartitioned)
	if err != nil || !isPartitioned {
		return
	}

	now := time.Now().UTC()
	currentMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthsAhead := 3

	for i := 0; i <= monthsAhead; i++ {
		partStart := currentMonth.AddDate(0, i, 0)
		partEnd := partStart.AddDate(0, 1, 0)
		partName := "syslog_logs_" + partStart.Format("2006_01")

		_, err := db.Exec(
			fmt.Sprintf(
				"CREATE TABLE IF NOT EXISTS %s PARTITION OF syslog_logs FOR VALUES FROM (%s) TO (%s)",
				partName,
				fmt.Sprintf("'%s'", partStart.Format(time.RFC3339)),
				fmt.Sprintf("'%s'", partEnd.Format(time.RFC3339)),
			),
		)
		if err != nil {
			slog.Error("partition creation failed", "partition", partName, "err", err)
		} else {
			slog.Info("partition ensured", "partition", partName)
		}
	}
}
