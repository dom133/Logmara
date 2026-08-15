package tailer

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"logmara/control"
	"logmara/model"
	"logmara/sharedstate"
)

// FileReader opens the log file, scans lines, and publishes serialized
// QueueEntry messages to the RabbitMQ queue. It runs only on the VIP leader.
func FileReader(ctx context.Context, db *sql.DB, filePath string, queue *sharedstate.Queue,
	flushTracker *sharedstate.FlushTracker, ic control.IngestionController,
	reopenLogFile func() error, sharedClient *sharedstate.Client) {

	posFile := filepath.Join(filepath.Dir(filePath), positionFileName)
	filePos, _ := loadStartPositionFromReader(db, filePath, posFile, sharedClient)

	defer func() {
		slog.Info("file reader stopped")
	}()

	for {
		if ctx.Err() != nil {
			slog.Info("file reader stopping")
			return
		}

		f, err := os.OpenFile(filePath, os.O_RDWR, 0644)
		if err != nil {
			slog.Warn("file reader: log file not available, retrying", "path", filePath, "error", err)
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

		if _, err := f.Seek(filePos, 0); err != nil {
			f.Close()
			if !sleepOrDone(ctx, 1*time.Second) {
				return
			}
			continue
		}

		// Backseek validation
		if filePos > 0 {
			var checkByte [1]byte
			if _, err := f.ReadAt(checkByte[:], filePos-1); err == nil && checkByte[0] != '\n' {
				var seekPos int64 = filePos - 1
				for seekPos > 0 {
					if _, err := f.ReadAt(checkByte[:], seekPos-1); err != nil {
						break
					}
					if checkByte[0] == '\n' {
						break
					}
					seekPos--
				}
				slog.Warn("file reader: position was mid-line, backseeking", "was", filePos, "now", seekPos)
				filePos = seekPos
				if _, err := f.Seek(seekPos, 0); err != nil {
					f.Close()
					if !sleepOrDone(ctx, 1*time.Second) {
						return
					}
					continue
				}
			}
		}

		scanner := bufio.NewScanner(f)
		buf := make([]byte, 0, maxLineSize)
		scanner.Buffer(buf, maxLineSize)
		splitter := &lineSplitter{}
		scanner.Split(splitter.split)

		curFilePos := filePos
		seq := flushTracker.NextSeq()

		for scanner.Scan() {
			if ic.IsPaused() {
				slog.Info("file reader: ingestion paused, breaking scan")
				break
			}

			line := scanner.Text()
			if line == "" {
				curFilePos += splitter.lastAdvance
				continue
			}

			curFilePos += splitter.lastAdvance

			// Check pause and backpressure every 100 lines so we stop
			// publishing quickly when purge pauses ingestion.
			if seq%100 == 0 {
				if ic.IsPaused() {
					slog.Info("file reader: ingestion paused during scan, breaking")
					break
				}
				if queue.IsFull(ctx) {
					time.Sleep(100 * time.Millisecond)
				}
			}

			entry := sharedstate.QueueEntry{
				Seq:     seq,
				NextPos: curFilePos,
				Line:    line,
			}
			seq++

			data, err := json.Marshal(entry)
			if err != nil {
				slog.Error("file reader: marshal error", "error", err)
				continue
			}

			if err := queue.Publish(ctx, data); err != nil {
				slog.Error("file reader: publish error", "error", err)
				f.Close()
				if !sleepOrDone(ctx, 1*time.Second) {
					return
				}
				goto reconnect
			}

			// Advance filePos immediately after successful publish so that
			// a break (pause / backpressure) or reconnect won't re-publish
			// already-sent lines and create duplicates.
			filePos = curFilePos
		}

		if err := scanner.Err(); err != nil {
			slog.Error("file reader: scan error", "error", err)
		}
		f.Close()

	reconnect:
		if !sleepOrDone(ctx, 200*time.Millisecond) {
			return
		}
	}
}

func dbFallbackPosition(db *sql.DB, filePath string) int64 {
	var lastTs *time.Time
	if err := db.QueryRow("SELECT max(timestamp) FROM syslog_logs").Scan(&lastTs); err != nil {
		slog.Error("db fallback query error", "error", err)
		return 0
	}
	if lastTs == nil {
		slog.Info("db empty, starting from beginning")
		return 0
	}

	f, err := os.Open(filePath)
	if err != nil {
		slog.Error("cannot open file for db fallback", "error", err)
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 1024*1024)
	pos := int64(0)
	lineLen := int64(0)
	for scanner.Scan() {
		line := scanner.Text()
		lineLen = int64(len(line))+1

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
	return pos
}

func loadStartPositionFromReader(db *sql.DB, filePath, posFile string, sharedClient *sharedstate.Client) (filePos, flushedPos int64) {
	if sharedClient != nil {
		if pos, flushed, ok := sharedClient.LoadTailerPosition(); ok {
			if f, err := os.Open(filePath); err == nil {
				stat, _ := f.Stat()
				f.Close()
				if pos <= stat.Size() {
					slog.Info("restored position from Redis", "pos", pos)
					return pos, flushed
				}
				slog.Warn("Redis position exceeds file size", "pos", pos, "fileSize", stat.Size())
			}
		}
	}

	if pos, fp, ok := loadPosition(posFile); ok {
		if f, err := os.Open(filePath); err == nil {
			stat, _ := f.Stat()
			f.Close()
			if pos <= stat.Size() {
				if curFp, fpOK := positionFingerprint(filePath, pos); fpOK && curFp == fp {
					slog.Info("restored position from file", "pos", pos)
					return pos, pos
				}
				slog.Warn("saved position content no longer matches", "pos", pos)
			}
		}
		slog.Info("saved position invalid, falling back to DB")
	}

	if db != nil {
		if pos := dbFallbackPosition(db, filePath); pos > 0 {
			slog.Info("restored position from DB fallback", "pos", pos)
			return pos, pos
		}
	}

	slog.Info("no valid position found, starting from 0")
	return 0, 0
}
