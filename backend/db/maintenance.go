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

func StartMaintenance(ctx context.Context, db *sql.DB) (func(), func()) {
	vacuumInterval := getIntervalHours("VACUUM_INTERVAL_HOURS", "vacuum_interval_hours", 24)
	mvInterval := getIntervalMinutes("MV_REFRESH_INTERVAL_MIN", "mv_refresh_interval_min", 30)

	createPartitions(db)
	stopVacuum := startVacuumScheduler(ctx, db, vacuumInterval)
	stopMV := startMVScheduler(ctx, db, mvInterval)

	return stopVacuum, stopMV
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
				refreshMV(db)
			}
		}
	}()
	return func() {
		<-done
	}
}

func runVacuumAnalyze(db *sql.DB) {
	slog.Info("running VACUUM ANALYZE", "table", "syslog_logs")
	_, err := db.Exec("VACUUM ANALYZE syslog_logs")
	if err != nil {
		slog.Error("vacuum analyze failed", "err", err)
		return
	}
	slog.Info("VACUUM ANALYZE completed", "table", "syslog_logs")
}

func refreshMV(db *sql.DB) {
	slog.Info("refreshing materialized views")
	for _, mv := range []string{"mv_dashboard_summary", "mv_dashboard_severity", "mv_timeline_hourly"} {
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