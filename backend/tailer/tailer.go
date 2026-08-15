package tailer

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/lib/pq"
	"syslog-gui/control"
	"syslog-gui/model"
	"syslog-gui/parser"
	"syslog-gui/sharedstate"
)

const (
	compactionInterval = 30 * time.Minute
	maxFileSize        = 100 * 1024 * 1024 // 100 MB
	positionFileName   = ".tailer_pos"
)

// Run starts the log tailer. When elector is nil (single-server/single-
// replica deployments, i.e. Redis not configured), it runs the ingestion
// loop directly and unconditionally - exactly like the original Start did.
// When elector is set (multiple api replicas sharing the same log file over
// NFS), only the replica that currently holds the elected lock actually
// tails/flushes/compacts; the others wait, ready to take over the moment
// the lock becomes available (leader crash, node loss, etc.).
func Run(ctx context.Context, db *sql.DB, filePath string, engine *parser.Engine, ic control.IngestionController, elector *sharedstate.LeaderElector) {
	if elector == nil {
		runIngestionLoop(ctx, db, filePath, engine, ic)
		return
	}
	runWithLeaderElection(ctx, db, filePath, engine, ic, elector)
}

func runWithLeaderElection(ctx context.Context, db *sql.DB, filePath string, engine *parser.Engine, ic control.IngestionController, elector *sharedstate.LeaderElector) {
	const retryInterval = 5 * time.Second
	const renewInterval = 5 * time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		if !elector.Acquire(ctx) {
			if !sleepOrDone(ctx, retryInterval) {
				return
			}
			continue
		}

		slog.Info("tailer: acquired leader lock, starting ingestion")
		leaderCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			runIngestionLoop(leaderCtx, db, filePath, engine, ic)
		}()

	renewLoop:
		for {
			select {
			case <-ctx.Done():
				cancel()
				<-done
				elector.Release(context.Background())
				return
			case <-time.After(renewInterval):
				if !elector.Renew(ctx) {
					slog.Warn("tailer: lost leader lock, stepping down")
					cancel()
					<-done
					break renewLoop
				}
			}
		}
	}
}

// sleepOrDone waits for d or until ctx is cancelled, whichever comes first.
// Returns false if ctx was cancelled, so callers can bail out promptly
// instead of finishing out the full sleep.
func sleepOrDone(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func runIngestionLoop(ctx context.Context, db *sql.DB, filePath string, engine *parser.Engine, ic control.IngestionController) {
	slog.Info("file tailer started", "path", filePath)
	batchSize := 500
	batchInterval := 2 * time.Second

	posFile := filepath.Join(filepath.Dir(filePath), positionFileName)
	filePos, flushedPos := loadStartPosition(db, filePath, posFile)

	var entries []model.IngestEntry
	lastFlush := time.Now()
	lastCompaction := time.Now()

	for {
		if ctx.Err() != nil {
			slog.Info("file tailer stopping")
			return
		}

		f, err := os.OpenFile(filePath, os.O_RDWR, 0644)
		if err != nil {
			slog.Error("waiting for file", "path", filePath, "error", err)
			if !sleepOrDone(ctx, 2*time.Second) {
				return
			}
			continue
		}

		stat, err := f.Stat()
		if err != nil {
			f.Close()
			if !sleepOrDone(ctx, 2*time.Second) {
				return
			}
			continue
		}

		fileSize := stat.Size()
		if filePos > fileSize {
			filePos = 0
			slog.Info("file rotated, resetting position")
		}

		// Periodic compaction: remove data already flushed to DB
		if (time.Since(lastCompaction) > compactionInterval || fileSize > maxFileSize) && fileSize > flushedPos*2 {
			if err := compactFile(f, flushedPos, filePath); err != nil {
				slog.Error("compaction error", "error", err)
			} else {
				filePos = 0
				flushedPos = 0
				savePosition(posFile, 0)
				stat, _ = f.Stat()
				fileSize = stat.Size()
				lastCompaction = time.Now()
			}
		}

		if _, err := f.Seek(filePos, 0); err != nil {
			f.Close()
			if !sleepOrDone(ctx, 1*time.Second) {
				return
			}
			continue
		}

		scanner := bufio.NewScanner(f)
		buf := make([]byte, 0, 1024*1024)
		scanner.Buffer(buf, 1024*1024)

		batchStartPos := filePos
		scanned := false

		for scanner.Scan() {
			if ic.IsPaused() {
				break
			}
			line := scanner.Text()
			if line == "" {
				continue
			}

			scanned = true

			var entry model.IngestEntry
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				slog.Error("invalid JSON", "error", err)
				entry = model.IngestEntry{
					Timestamp: time.Now().Format(time.RFC3339),
					Hostname:  "unknown",
					Severity:  "error",
					Message:   fmt.Sprintf("[MALFORMED JSON] %s", line),
				}
			}

			if entry.Hostname == "" {
				continue
			}

			appName := entry.AppName
			result := engine.Parse(entry.Hostname, appName, entry.Message)
			if result != nil {
				jsonData, err := json.Marshal(result.Fields)
				if err == nil {
					entry.ParsedFields = jsonData
				}
				entry.MatchedParsers = result.Parsers
			}

			entries = append(entries, entry)

			now := time.Now()
			if len(entries) >= batchSize || now.Sub(lastFlush) >= batchInterval {
				if !ic.IsPaused() {
					if err := flushBatch(db, entries); err != nil {
						slog.Error("flush error", "error", err)
					} else {
						flushedPos = batchStartPos
						savePosition(posFile, flushedPos)
					}
				}
				entries = entries[:0]
				batchStartPos = filePos
				lastFlush = now
			}
		}

		if scanned {
			curPos, err := f.Seek(0, 2)
			if err == nil {
				filePos = curPos
			}
		}
		f.Close()

		if len(entries) > 0 && !ic.IsPaused() {
			if err := flushBatch(db, entries); err != nil {
				slog.Error("flush error", "error", err)
			} else {
				flushedPos = batchStartPos
				savePosition(posFile, flushedPos)
			}
			entries = entries[:0]
		}

		if !sleepOrDone(ctx, 200*time.Millisecond) {
			return
		}
	}
}

func compactFile(f *os.File, flushedPos int64, filePath string) error {
	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	fileSize := stat.Size()

	if flushedPos >= fileSize {
		slog.Info("nothing to compact", "flushedPos", flushedPos, "fileSize", fileSize)
		return nil
	}

	// Read unprocessed data (from flushedPos to EOF)
	if _, err := f.Seek(flushedPos, 0); err != nil {
		return fmt.Errorf("seek to flushedPos: %w", err)
	}
	remaining, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("read remaining: %w", err)
	}

	slog.Info("compacting file", "path", filePath, "fileSize", fileSize, "remaining", len(remaining), "flushedPos", flushedPos)

	// Truncate to 0
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("truncate: %w", err)
	}

	// Write back unprocessed data
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("seek to start: %w", err)
	}
	if _, err := f.Write(remaining); err != nil {
		return fmt.Errorf("write remaining: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	slog.Info("compaction done", "remaining", len(remaining))
	return nil
}

func flushBatch(db *sql.DB, entries []model.IngestEntry) error {
	if len(entries) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	query := `INSERT INTO syslog_logs (timestamp, hostname, fromhost_ip, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, matched_parsers)
		          VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
	stmt, err := tx.Prepare(query)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	ingested := 0
	for _, entry := range entries {
		ts, err := parseTimestamp(entry.Timestamp)
		if err != nil {
			ts = time.Now()
		}

		fromHostIP := nullStr(entry.FromHostIP)
		appName := nullStr(entry.AppName)
		processID := nullStr(entry.ProcessID)
		msgID := nullStr(entry.MsgID)
		facility := nullStr(entry.Facility)
		rawMsg := nullStr(entry.RawMessage)
		parsedFields := json.RawMessage("{}")
		if len(entry.ParsedFields) > 0 {
			parsedFields = entry.ParsedFields
		}

		_, err = stmt.Exec(ts, entry.Hostname, fromHostIP, appName, processID, msgID,
			entry.Severity, facility, entry.Message, rawMsg, parsedFields, pq.StringArray(entry.MatchedParsers))
		if err != nil {
			slog.Error("insert error", "error", err)
			continue
		}
		ingested++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	if ingested > 0 {
		slog.Info("flushed logs", "count", ingested)
	}

	return nil
}

func parseTimestamp(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"Jan  2 15:04:05",
		"Jan  2 03:04:05",
		time.UnixDate,
	}

	for _, format := range formats {
		t, err := time.Parse(format, s)
		if err == nil {
			return t, nil
		}
	}

	return time.Now(), fmt.Errorf("unparseable: %s", s)
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func savePosition(path string, pos int64) {
	if err := os.WriteFile(path, []byte(strconv.FormatInt(pos, 10)), 0644); err != nil {
		slog.Error("save position error", "error", err)
	}
}

func loadPosition(path string) (int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pos, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return 0, false
	}
	return pos, true
}

func loadStartPosition(db *sql.DB, filePath, posFile string) (filePos, flushedPos int64) {
	if pos, ok := loadPosition(posFile); ok {
		if f, err := os.Open(filePath); err == nil {
			stat, _ := f.Stat()
			f.Close()
			if pos <= stat.Size() {
				slog.Info("restored position from file", "pos", pos)
				return pos, pos
			}
		}
		slog.Info("saved position invalid, falling back to DB")
	}

	var lastTs *time.Time
	if err := db.QueryRow("SELECT max(timestamp) FROM syslog_logs").Scan(&lastTs); err != nil {
		slog.Error("db fallback query error", "error", err)
		return 0, 0
	}
	if lastTs == nil {
		slog.Info("db empty, starting from beginning")
		return 0, 0
	}

	f, err := os.Open(filePath)
	if err != nil {
		slog.Error("cannot open file for db fallback", "error", err)
		return 0, 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 1024*1024)
	pos := int64(0)
	lineLen := int64(0)
	for scanner.Scan() {
		line := scanner.Text()
		lineLen = int64(len(line)) + 1

		var entry model.IngestEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			pos += lineLen
			continue
		}
		ts, err := parseTimestamp(entry.Timestamp)
		if err != nil {
			pos += lineLen
			continue
		}
		if ts.After(*lastTs) {
			break
		}
		pos += lineLen
	}

	slog.Info("restored position from DB", "lastTs", lastTs.Format(time.RFC3339), "pos", pos)
	return pos, pos
}
