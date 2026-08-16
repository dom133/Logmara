package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

func StartMaintenance(ctx context.Context, db *sql.DB) (func(), func(), func(), func(), func(), func()) {
	vacuumInterval := getIntervalHours("VACUUM_INTERVAL_HOURS", 24)
	mvInterval := getIntervalMinutes("MV_REFRESH_INTERVAL_MIN", 30)
	partitionInterval := getIntervalHours("PARTITION_CREATE_INTERVAL_HOURS", 24)

	// StartMaintenance itself runs synchronously on main's startup path,
	// before the real HTTP listener binds (see main.go's <-schemaReady wait)
	// - createPartitions can take anywhere from milliseconds to minutes
	// depending on lock contention on syslog_logs (autovacuum, MV refresh,
	// insert traffic), so calling it inline here would make replica startup
	// (and therefore /api/health) hostage to that. Fire the initial run the
	// same way the recurring scheduler does: in the background, off this
	// call's critical path.
	go createPartitions(db)
	stopPartitions := startPartitionScheduler(ctx, db, partitionInterval)
	stopVacuum := startVacuumScheduler(ctx, db, vacuumInterval)
	stopMV := startMVScheduler(ctx, db, mvInterval)
	stopTokenCleanup := startRefreshTokenCleanupScheduler(ctx, db, 1*time.Hour)
	stopJWTCleanup := startJWTBlacklistCleanup(ctx, db, 1*time.Minute)
	stopArchive := startArchiveCleanupScheduler(ctx, db, 12*time.Hour)

	return stopVacuum, stopMV, stopTokenCleanup, stopJWTCleanup, stopArchive, stopPartitions
}

// startPartitionScheduler periodically re-runs createPartitions so the
// ahead-of-time partition window stays filled for the lifetime of a
// long-running process, not just at startup. This matters far more at daily
// granularity than the old fixed monthly scheme: 3 months of pre-created
// monthly partitions comfortably outlasted any realistic uptime between
// restarts, but daily partitions only look a handful of days ahead by
// default (see partitionGranularity.aheadCount), so a process staying up
// past that window would otherwise start spilling new logs into the
// unpartitioned default partition.
func startPartitionScheduler(ctx context.Context, db *sql.DB, interval time.Duration) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		slog.Info("partition scheduler started", "interval_hours", interval.Hours())
		for {
			select {
			case <-ctx.Done():
				slog.Info("partition scheduler stopped")
				close(done)
				return
			case <-ticker.C:
				createPartitions(db)
			}
		}
	}()
	return func() {
		<-done
	}
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
				RefreshMV(ctx, db)
				RefreshDeviceStatsMV(ctx, db)
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
func startJWTBlacklistCleanup(ctx context.Context, db *sql.DB, interval time.Duration) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		slog.Info("jwt blacklist cleanup scheduler started", "interval_minutes", interval.Minutes())
		for {
			select {
			case <-ctx.Done():
				slog.Info("jwt blacklist cleanup scheduler stopped")
				close(done)
				return
			case <-ticker.C:
				CleanupExpiredBlacklist(db)
			}
		}
	}()
	return func() {
		<-done
	}
}

// startArchiveCleanupScheduler periodically removes archived logs older than
// the retention period configured in app_settings (default: 30 days).
func startArchiveCleanupScheduler(ctx context.Context, db *sql.DB, interval time.Duration) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		slog.Info("archive cleanup scheduler started", "interval_hours", interval.Hours())
		for {
			select {
			case <-ctx.Done():
				slog.Info("archive cleanup scheduler stopped")
				close(done)
				return
			case <-ticker.C:
				cleanupArchivedLogs(db)
			}
		}
	}()
	return func() {
		<-done
	}
}

// cleanupArchivedLogs deletes logs older than the configured retention period.
func cleanupArchivedLogs(db *sql.DB) {
	retentionDays := 30
	if val := GetSetting(db, "retention_days", "30"); val != "" {
		if d, err := strconv.Atoi(val); err == nil {
			retentionDays = d
		}
	}

	n, err := CleanupOldLogs(db, retentionDays)
	if err != nil {
		slog.Error("archive cleanup failed", "err", err)
		return
	}
	if n > 0 {
		slog.Info("archive cleanup completed", "rows_deleted", n, "retention_days", retentionDays)
	}
}

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

	// "Remember this device" tokens (remember = true) are exempt from the
	// short inactivity timeout: they're meant to keep a session alive
	// across days/weeks of not opening the app, not just within a single
	// active-browsing window.
	timeoutMin := getInactivityTimeoutMin(db)
	res, err = db.Exec(
		"UPDATE refresh_tokens SET used = true, used_at = NOW() WHERE used = false AND remember = false AND COALESCE(last_used_at, created_at) < NOW() - ($1 || ' minutes')::INTERVAL",
		timeoutMin,
	)
	if err != nil {
		slog.Error("inactive token expiry failed", "err", err)
		return
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		slog.Info("inactive tokens expired", "rows_marked", n, "timeout_min", timeoutMin)
	}

	// Expire remembered sessions that have exceeded the configured max
	// lifetime (session_remembered_max_days). This prevents a remembered
	// token from living forever if the admin lowers the max TTL.
	rememberedMaxDays := getRememberedMaxDays(db)
	res, err = db.Exec(
		"UPDATE refresh_tokens SET used = true, used_at = NOW() WHERE used = false AND remember = true AND created_at < NOW() - ($1 || ' days')::INTERVAL",
		rememberedMaxDays,
	)
	if err != nil {
		slog.Error("remembered token max TTL expiry failed", "err", err)
		return
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		slog.Info("remembered tokens expired by max TTL", "rows_marked", n, "max_days", rememberedMaxDays)
	}
}

func getInactivityTimeoutMin(db *sql.DB) string {
	var val sql.NullString
	err := db.QueryRow("SELECT value FROM app_settings WHERE key = 'session_timeout_min'").Scan(&val)
	if err != nil || !val.Valid {
		return "15"
	}
	return val.String
}

func getRememberedMaxDays(db *sql.DB) string {
	var val sql.NullString
	err := db.QueryRow("SELECT value FROM app_settings WHERE key = 'session_remembered_max_days'").Scan(&val)
	if err != nil || !val.Valid {
		return "60"
	}
	return val.String
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

	g := activePartitionGranularity(db)
	current := g.truncate(time.Now().UTC())
	names := make([]string, 0, 2)
	for _, t := range []time.Time{g.previous(current), current} {
		name := g.partitionName(t)
		var exists bool
		if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_class WHERE relname = $1)", name).Scan(&exists); err == nil && exists {
			names = append(names, name)
		}
	}
	return names
}

// HasActiveSession reports whether any user currently holds a usable
// session - a refresh token that hasn't been consumed (by logout or by
// being rotated on refresh) and hasn't expired. Used to gate the fast (30s)
// dashboard MV refresh loop: no point keeping stats near-real-time when
// nobody is logged in to look at them.
func HasActiveSession(db *sql.DB) bool {
	var active bool
	err := db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM refresh_tokens
			WHERE used = false
			  AND expires_at > NOW()
			  AND COALESCE(last_used_at, created_at) > NOW() - (COALESCE((SELECT value FROM app_settings WHERE key = 'session_timeout_min'), '15')::int || ' minutes')::INTERVAL
		)
	`).Scan(&active)
	if err != nil {
		slog.Error("active session check failed", "err", err)
		return false
	}
	return active
}

func RefreshMV(ctx context.Context, db *sql.DB) {
	// Migrate() runs in its own goroutine and can take a while on a large
	// table (index builds, mv_device_stats aggregation) - the periodic
	// refresh tickers start immediately regardless, so without this guard
	// they can fire before Migrate() has created the views they're trying
	// to refresh.
	if IsAppStarting() {
		slog.Info("skipping materialized view refresh - migration still in progress")
		return
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		slog.Error("mv refresh: acquire connection failed", "err", err)
		return
	}
	defer conn.Close()

	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", mvRefreshLockKey).Scan(&acquired); err != nil {
		slog.Error("mv refresh: advisory lock check failed", "err", err)
		return
	}
	if !acquired {
		slog.Info("mv refresh already running elsewhere, skipping this cycle")
		return
	}
	defer func() {
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", mvRefreshLockKey); err != nil {
			slog.Warn("failed to release mv refresh advisory lock", "err", err)
		}
	}()

	slog.Info("refreshing materialized views")
	// mv_device_stats refreshes on its own cadence (see RefreshDeviceStatsMV)
	// - its dev_parsers CTE (unnest+array_agg over every row) is far more
	// expensive than these four, so bundling it into the same cycle meant it
	// was very often still running when the next 30s tick fired.
	for _, mv := range []string{"mv_dashboard_summary", "mv_dashboard_severity", "mv_timeline_hourly", "mv_dashboard_top_errors"} {
		if err := refreshMaterializedView(ctx, db, mv); err != nil {
			slog.Error("mv refresh failed", "view", mv, "err", err)
		} else {
			slog.Info("materialized view refreshed", "view", mv)
		}
	}
}

// RefreshDeviceStatsMV refreshes mv_device_stats behind its own advisory
// lock, separate from RefreshMV's four lighter views - see the comment on
// deviceStatsMVRefreshLockKey and the call site in main.go for why it runs
// on a longer, independent cadence.
func RefreshDeviceStatsMV(ctx context.Context, db *sql.DB) {
	if IsAppStarting() {
		return
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		slog.Error("device stats mv refresh: acquire connection failed", "err", err)
		return
	}
	defer conn.Close()

	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", deviceStatsMVRefreshLockKey).Scan(&acquired); err != nil {
		slog.Error("device stats mv refresh: advisory lock check failed", "err", err)
		return
	}
	if !acquired {
		slog.Info("device stats mv refresh already running elsewhere, skipping this cycle")
		return
	}
	defer func() {
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", deviceStatsMVRefreshLockKey); err != nil {
			slog.Warn("failed to release device stats mv refresh advisory lock", "err", err)
		}
	}()

	if err := refreshMaterializedView(ctx, db, "mv_device_stats"); err != nil {
		slog.Error("mv refresh failed", "view", "mv_device_stats", "err", err)
	} else {
		slog.Info("materialized view refreshed", "view", "mv_device_stats")
	}
}

func getIntervalHours(envKey string, defaultHours int) time.Duration {
	if v := os.Getenv(envKey); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h > 0 {
			return time.Duration(h) * time.Hour
		}
	}
	return time.Duration(defaultHours) * time.Hour
}

func getIntervalMinutes(envKey string, defaultMinutes int) time.Duration {
	if v := os.Getenv(envKey); v != "" {
		if m, err := strconv.Atoi(v); err == nil && m > 0 {
			return time.Duration(m) * time.Minute
		}
	}
	return time.Duration(defaultMinutes) * time.Minute
}

// partitionLockKey is a separate advisory lock key from migrationLockKey (see
// db.go) - createPartitions and the schema migration are independent
// operations that shouldn't block on each other, just serialize against
// concurrent copies of themselves.
const partitionLockKey = 8743012

// mvRefreshLockKey/deviceStatsMVRefreshLockKey are further advisory lock
// keys, one per refresh cadence (see RefreshMV/RefreshDeviceStatsMV below).
// REFRESH MATERIALIZED VIEW CONCURRENTLY takes a lock on the view being
// refreshed, so this process's own 30s/30min refresh tickers - and the same
// tickers on every other replica - were observed racing each other and
// queuing behind that lock for up to 140s+ per attempt, exactly like
// createPartitions above before it got the same treatment. pg_try_advisory_lock
// lets every loser skip its cycle outright instead of piling up.
const mvRefreshLockKey = 8743013
const deviceStatsMVRefreshLockKey = 8743014

// createPartitions pre-creates syslog_logs partitions, under whichever
// granularity is already active (see activePartitionGranularity - not
// necessarily PARTITION_INTERVAL's current value), from the current period
// through granularity.aheadCount periods ahead. That keeps inserts from
// falling back to the unpartitioned default partition just because nobody's
// created tomorrow's (or next month's) table yet. Called once at startup on
// every replica and then on a recurring schedule (see
// startPartitionScheduler) - the ahead window is a buffer against a
// long-running process, not something startup alone can keep filled at
// daily granularity.
//
// pg_try_advisory_lock serializes this against the same call on other
// replicas: CREATE TABLE ... PARTITION OF takes a lock on the syslog_logs
// parent, so replicas racing to create (or, more often once ahead-of-time
// partitions already exist, to redundantly no-op against) the same
// partitions were observed queuing behind each other for 60-140s+ per
// attempt. Only one replica doing the work at a time, with the rest
// skipping the cycle outright, avoids that pile-up; anything a skipped
// replica would have created is already covered by the winner within the
// same cycle, and the next scheduled run picks up regardless.
func createPartitions(db *sql.DB) {
	isPartitioned := false
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_class WHERE relname = 'syslog_logs' AND relkind = 'p')`).Scan(&isPartitioned)
	if err != nil || !isPartitioned {
		return
	}

	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		slog.Error("partition creation: acquire connection failed", "err", err)
		return
	}
	defer conn.Close()

	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", partitionLockKey).Scan(&acquired); err != nil {
		slog.Error("partition creation: advisory lock check failed", "err", err)
		return
	}
	if !acquired {
		slog.Info("partition maintenance already running on another replica, skipping this cycle")
		return
	}
	defer func() {
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", partitionLockKey); err != nil {
			slog.Warn("failed to release partition advisory lock", "err", err)
		}
	}()

	g := activePartitionGranularity(db)
	partStart := g.truncate(time.Now().UTC())

	for i := 0; i <= g.aheadCount; i++ {
		partEnd := g.next(partStart)
		partName := g.partitionName(partStart)

		_, err := conn.ExecContext(ctx,
			fmt.Sprintf(
				"CREATE TABLE IF NOT EXISTS %s PARTITION OF syslog_logs FOR VALUES FROM (%s) TO (%s)",
				partName,
				fmt.Sprintf("'%s'", partStart.Format(time.RFC3339)),
				fmt.Sprintf("'%s'", partEnd.Format(time.RFC3339)),
			),
		)
		if err != nil {
			// A database that switched PARTITION_INTERVAL (e.g. month -> day)
			// still has its old, coarser partition covering "now" until that
			// partition's own end date - a day/week/etc. within it isn't
			// missing a partition, it's already covered by the wider one, and
			// Postgres reports that as an overlap rather than IF NOT EXISTS
			// treating it as a no-op (the names differ, so IF NOT EXISTS
			// doesn't apply). Not an actual problem: inserts land in the
			// existing partition fine, and this stops erroring on its own
			// once "now" advances past the old partition's end.
			if strings.Contains(err.Error(), "would overlap partition") {
				slog.Info("partition range already covered by an existing partition, skipping", "partition", partName, "err", err)
			} else {
				slog.Error("partition creation failed", "partition", partName, "err", err)
			}
		} else {
			slog.Info("partition ensured", "partition", partName)
		}

		partStart = partEnd
	}
}
