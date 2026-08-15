package tailer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lib/pq"
	"logmara/alertengine"
	"logmara/control"
	"logmara/model"
	"logmara/parser"
	"logmara/sharedstate"
)

// sanitizeForPostgres repairs invalid UTF-8 byte sequences (a misbehaving
// device sending non-UTF-8 encoded text) and strips embedded NUL bytes. NUL
// is otherwise perfectly valid UTF-8 (U+0000), but Postgres's text/jsonb
// storage rejects it outright ("invalid byte sequence for encoding UTF8:
// 0x00") regardless of the surrounding text's validity - and because
// flushBatch commits a whole batch in one transaction, a single such value
// anywhere in a batch aborted the transaction and silently dropped every
// other (perfectly valid) entry queued alongside it.
func sanitizeForPostgres(s string) string {
	if s == "" {
		return s
	}
	if !strings.ContainsRune(s, 0) && utf8.ValidString(s) {
		return s
	}
	return strings.ReplaceAll(strings.ToValidUTF8(s, "�"), "\x00", "")
}

// truncatedISORe matches app_name values that are actually truncated ISO 8601
// timestamps (e.g. "2026-07-23T20") caused by rsyslog mis-parsing non-RFC3164
// syslog headers from certain UniFi devices.
var truncatedISORe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}$`)

const (
	compactionInterval = 30 * time.Minute
	maxFileSize        = 100 * 1024 * 1024 // 100 MB
	positionFileName   = ".tailer_pos"
	maxLineSize        = 10 * 1024 * 1024 // cap a single ingested "line" at 10MB
)

// lineSplitter behaves like bufio.ScanLines, with two differences:
//
//   - if no newline shows up within maxLineSize bytes it force-cuts a token
//     there instead of growing the buffer further. Plain ScanLines would
//     instead make Scan() return bufio.ErrTooLong and stop for good - since
//     every device's syslog output is interleaved into one shared file
//     processed strictly in order, a single oversized/newline-less message
//     (crafted or just a device bug) would otherwise wedge the scanner at
//     that byte offset forever, blocking ingestion for every other host
//     sharing the file. Forcing progress instead turns it into a handful of
//     "[MALFORMED JSON]" entries for that one device.
//
//   - unlike ScanLines, it never force-emits a trailing chunk just because
//     atEOF is true and no newline has shown up yet. For a file being
//     tailed live, atEOF only means "no more bytes are available right
//     now", not "this file is finished growing" - rsyslog may simply still
//     be mid-write on that last line. Waiting instead of force-cutting
//     lets the same still-on-disk bytes be re-read whole (now complete) on
//     the next poll, instead of splitting one good record into two bogus
//     "[MALFORMED JSON]" halves.
//
// lastAdvance records the exact number of bytes the most recent call
// returned as advance - bufio.Scanner has no other way to expose a
// SplitFunc's own advance value to its caller, and runIngestionLoop needs it
// to track the file position by what Scan() actually consumed rather than
// by asking the OS file descriptor how far it has physically read ahead
// into the Scanner's internal buffer (see the comment on curFilePos there).
type lineSplitter struct {
	lastAdvance int64
}

func (s *lineSplitter) split(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		s.lastAdvance = int64(i + 1)
		return i + 1, data[0:i], nil
	}
	if len(data) >= maxLineSize {
		s.lastAdvance = maxLineSize
		return maxLineSize, data[0:maxLineSize], nil
	}
	return 0, nil, nil
}

// Run starts the log tailer. When elector is nil (single-server/single-
// replica deployments, i.e. Redis not configured), it runs the ingestion
// loop directly and unconditionally - exactly like the original Start did.
// When elector is set (multiple api replicas sharing the same log file over
// NFS), only the replica that currently holds the elected lock actually
// tails/flushes/compacts; the others wait, ready to take over the moment
// the lock becomes available (leader crash, node loss, etc.).
// reopenLogFile is called after compactFile atomically replaces filePath via
// rename, so rsyslog (a separate, uncoordinated process/container that's
// always the one actually appending to that shared file, in both
// docker-compose.yml and docker-stack.app.yml - see compactFile's doc
// comment) picks up the new inode instead of silently writing into the old,
// now-unlinked one forever. Callers are expected to pass a real function
// (main.go wires in handler.ReopenRsyslogLogFile); nil is only tolerated
// here for tests that don't exercise the compaction path.
func Run(ctx context.Context, db *sql.DB, filePath string, engine *parser.Engine, ic control.IngestionController, elector *sharedstate.LeaderElector, alerts *alertengine.Engine, rate sharedstate.RateCounter, reopenLogFile func() error) {
	if elector == nil {
		runIngestionLoop(ctx, db, filePath, engine, ic, alerts, rate, reopenLogFile)
		return
	}
	runWithLeaderElection(ctx, db, filePath, engine, ic, elector, alerts, rate, reopenLogFile)
}

const (
	leaderRetryInterval = 5 * time.Second
	leaderRetryJitter   = 1 * time.Second
	leaderRenewInterval = 5 * time.Second
	leaderMaxRenewFails = 3
	// startupJitterMax staggers each replica's very first Acquire attempt.
	// Not needed for correctness - Acquire's SetNX is atomic regardless of
	// how many replicas race it - but every replica typically starts within
	// the same second or two of a `docker stack deploy`/rescale, so without
	// this every one of them logs a failed acquire attempt at once. Purely
	// cosmetic log-noise reduction.
	startupJitterMax = 3 * time.Second
)

func runWithLeaderElection(ctx context.Context, db *sql.DB, filePath string, engine *parser.Engine, ic control.IngestionController, elector *sharedstate.LeaderElector, alerts *alertengine.Engine, rate sharedstate.RateCounter, reopenLogFile func() error) {
	startupJitter := time.Duration(rand.Int63n(int64(startupJitterMax)))
	if !sleepOrDone(ctx, startupJitter) {
		return
	}

	for {
		if ctx.Err() != nil {
			return
		}

		if !elector.Acquire(ctx) {
			jitter := time.Duration(rand.Int63n(int64(leaderRetryJitter)))
			if !sleepOrDone(ctx, leaderRetryInterval+jitter) {
				return
			}
			continue
		}

		slog.Info("tailer: acquired leader lock, starting ingestion")
		leaderCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			runIngestionLoop(leaderCtx, db, filePath, engine, ic, alerts, rate, reopenLogFile)
		}()

		consecutiveFails := 0

	renewLoop:
		for {
			select {
			case <-ctx.Done():
				cancel()
				<-done
				elector.Release(context.Background())
				return
			case <-time.After(leaderRenewInterval):
				ok, lost := elector.Renew(ctx)
				if lost {
					// Definitive: Redis confirmed another instance already
					// owns the lock, meaning it may already be ingesting.
					// Unlike a transient renew error below, this can never
					// be tolerated for extra cycles - every extra second
					// here is a window where two replicas tail the same
					// file concurrently.
					slog.Warn("tailer: lost leader lock to another instance, stepping down immediately")
					cancel()
					<-done
					break renewLoop
				}
				if !ok {
					consecutiveFails++
					slog.Warn("tailer: renew failed (transient), retrying", "consecutive", consecutiveFails, "threshold", leaderMaxRenewFails)
					if consecutiveFails >= leaderMaxRenewFails {
						slog.Warn("tailer: renew failing repeatedly, stepping down")
						cancel()
						<-done
						break renewLoop
					}
				} else {
					consecutiveFails = 0
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

func runIngestionLoop(ctx context.Context, db *sql.DB, filePath string, engine *parser.Engine, ic control.IngestionController, alerts *alertengine.Engine, rate sharedstate.RateCounter, reopenLogFile func() error) {
	slog.Info("file tailer started", "path", filePath)
	batchSize := 500
	batchInterval := 2 * time.Second

	posFile := filepath.Join(filepath.Dir(filePath), positionFileName)
	filePos, flushedPos := loadStartPosition(db, filePath, posFile)

	var entries []model.IngestEntry
	lastFlush := time.Now()
	lastCompaction := time.Now()
	batchStartPos := int64(0)

	defer func() {
		// Flush any remaining entries on shutdown
		if len(entries) > 0 {
			slog.Info("flushing remaining entries on shutdown", "count", len(entries))
			if err := flushBatch(db, entries, rate); err != nil {
				slog.Error("final flush error", "error", err)
			} else {
				flushedPos = batchStartPos
				savePosition(posFile, flushedPos, filePath)
				alerts.EvaluateBatch(db, entries)
			}
		}
		slog.Info("file tailer stopped")
	}()

	for {
		if ctx.Err() != nil {
			slog.Info("file tailer stopping")
			return
		}

		f, err := os.OpenFile(filePath, os.O_RDWR, 0644)
		if err != nil {
			slog.Warn("tailer: log file not available, retrying", "path", filePath, "error", err)
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
			newF, err := compactFile(f, flushedPos, filePath, reopenLogFile)
			if err != nil {
				slog.Error("compaction error", "error", err)
				// newF may be nil (compactFile already closed f and failed to
				// reopen) or the original, still-good f (an earlier step
				// failed) - either way, don't risk using a handle that might
				// be stale or closed; drop it and let the top of this loop
				// open a fresh one next pass.
				f.Close()
				if !sleepOrDone(ctx, 1*time.Second) {
					return
				}
				continue
			}
			f = newF
			filePos = 0
			flushedPos = 0
			savePosition(posFile, 0, filePath)
			lastCompaction = time.Now()
		}

		if _, err := f.Seek(filePos, 0); err != nil {
			f.Close()
			if !sleepOrDone(ctx, 1*time.Second) {
				return
			}
			continue
		}

		scanner := bufio.NewScanner(f)
		buf := make([]byte, 0, maxLineSize)
		scanner.Buffer(buf, maxLineSize)
		splitter := &lineSplitter{}
		scanner.Split(splitter.split)

		batchStartPos := filePos
		curFilePos := filePos
		scanned := false

		for scanner.Scan() {
			curFilePos += splitter.lastAdvance
			if ic.IsPaused() {
				slog.Info("tailer: ingestion paused, breaking scan")
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
				sanitizedLine := sanitizeForPostgres(line)
				entry = model.IngestEntry{
					Timestamp: time.Now().Format(time.RFC3339),
					Hostname:  "unknown",
					Severity:  "error",
					Message:   fmt.Sprintf("[MALFORMED JSON] %s", sanitizedLine),
				}
				alerts.EvaluateMalformedJSON(db, sanitizedLine)
			}

			// A device sending a NUL byte in its message unmarshals from JSON
			// just fine, but Postgres refuses to store it in any text/jsonb
			// column - sanitize every string field before it's used for
			// parsing or insertion, whichever branch above produced entry.
			entry.Hostname = sanitizeForPostgres(entry.Hostname)
			entry.FromHostIP = sanitizeForPostgres(entry.FromHostIP)
			entry.AppName = sanitizeForPostgres(entry.AppName)
			entry.ProcessID = sanitizeForPostgres(entry.ProcessID)
			entry.MsgID = sanitizeForPostgres(entry.MsgID)
			entry.Severity = sanitizeForPostgres(entry.Severity)
			entry.Facility = sanitizeForPostgres(entry.Facility)
			entry.Message = sanitizeForPostgres(entry.Message)
			entry.RawMessage = sanitizeForPostgres(entry.RawMessage)
			entry.ViaRelay = sanitizeForPostgres(entry.ViaRelay)

			if entry.Hostname == "" {
				continue
			}

			// Fix rsyslog mis-parse: when a device sends an ISO 8601 timestamp in its
			// syslog header (non-RFC3164), rsyslog treats the truncated date part
			// (e.g. "2026-07-23T20") as programname and the rest ("11:40.246Z ...")
			// as the message. Restore the original message by merging them back.
			if truncatedISORe.MatchString(entry.AppName) {
				fullMsg := entry.AppName + ":" + entry.Message
				entry.Message = fullMsg
				entry.RawMessage = fullMsg
				// If the restored message contains a known structured format, set app
				if idx := strings.Index(fullMsg, "CEF:"); idx >= 0 {
					entry.AppName = "CEF"
				} else {
					entry.AppName = ""
				}
			}

			appName := entry.AppName
			result := engine.Parse(entry.Hostname, appName, entry.Message)
			if result != nil {
				for k, v := range result.Fields {
					result.Fields[k] = sanitizeForPostgres(v)
				}
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
					if err := flushBatch(db, entries, rate); err != nil {
						slog.Error("flush error", "error", err)
					} else {
						flushedPos = curFilePos
						savePosition(posFile, flushedPos, filePath)
						alerts.EvaluateBatch(db, entries)
					}
				}
				entries = entries[:0]
				batchStartPos = curFilePos
				lastFlush = now
			}
		}

		if err := scanner.Err(); err != nil {
			slog.Error("tailer: scan error", "error", err)
		}

		if scanned {
			// curFilePos only ever advances by the exact byte length
			// lineSplitter.split reported consuming for a token it actually
			// returned - never by however far bufio's own internal
			// read-ahead buffer happened to reach. The old approach here
			// (f.Seek(0, 1), i.e. asking the OS file descriptor's own
			// SEEK_CUR position) reflected the latter instead: bufio reads
			// ahead in large chunks, so that position routinely sat past
			// the last line Scan() had actually handed back, especially
			// once a single Read() started pulling in more than one
			// buffered line at a time - which is exactly what happens under
			// high log volume. Resuming from that overshot position next
			// poll would land mid-line whenever rsyslog was still mid-write
			// on the last line in the file, splitting it into two bogus
			// "[MALFORMED JSON]" halves instead of the one real record it
			// always was.
			filePos = curFilePos
		}
		f.Close()

		if len(entries) > 0 && !ic.IsPaused() {
			if err := flushBatch(db, entries, rate); err != nil {
				slog.Error("flush error", "error", err)
			} else {
				flushedPos = batchStartPos
				savePosition(posFile, flushedPos, filePath)
				alerts.EvaluateBatch(db, entries)
			}
			entries = entries[:0]
		}
		if len(entries) > 0 && ic.IsPaused() {
			slog.Warn("tailer: skipping flush — ingestion paused", "entries", len(entries))
		}

		if !sleepOrDone(ctx, 200*time.Millisecond) {
			return
		}
	}
}

// compactFile drops the already-flushed prefix of filePath by writing the
// still-unflushed tail (from flushedPos to EOF) to a temp file in the same
// directory and atomically renaming it over filePath, instead of truncating
// filePath in place and rewriting it through the same handle. The in-place
// approach used to leave a window - the time between Truncate(0) succeeding
// and the rewrite finishing, not instantaneous over NFS - during which the
// shared file sat empty or half-written while rsyslog (a completely
// separate, uncoordinated process/container, see rsyslog/syslog.conf's
// omfile action) was still independently appending new syslog lines to it.
// Anything rsyslog wrote into that window landed at the truncated file's
// offset 0 too, and this function's own subsequent, non-append Write(0..)
// would then clobber/interleave with it - a container killed mid-rewrite
// (Swarm node failure, or SIGKILL past stop_grace_period during a rolling
// update) made it worse by leaving the truncation itself unfinished. Either
// way the next tail pass would hit corrupted bytes and log them as
// "[MALFORMED JSON]".
//
// rename() is atomic and closes that window entirely, but it swaps the
// directory entry to a new inode - it does not affect a file descriptor
// rsyslogd already has open on the old one, which would otherwise keep
// silently appending into now-unlinked, invisible, ultimately-lost data
// forever. reopenLogFile (handler.ReopenRsyslogLogFile in production) asks
// rsyslogd to reopen the path so its next write lands in the new file - see
// that function's doc comment for why SIGHUP, not a full restart, is enough.
//
// Returns the file handle the caller must use from here on: on success this
// is always a freshly reopened handle on the compacted file (the caller's
// old one is closed by this function, since it now points at unlinked
// content); on error it's either the caller's original handle (still valid -
// every failure mode before the rename leaves filePath completely
// untouched) or nil (the rename succeeded but reopening the now-renamed
// path failed, which also already closed the original handle) - callers
// must treat a non-nil error as "close whatever was returned, if anything,
// and start this pass over" rather than assume which case they got.
func compactFile(f *os.File, flushedPos int64, filePath string, reopenLogFile func() error) (*os.File, error) {
	stat, err := f.Stat()
	if err != nil {
		return f, fmt.Errorf("stat: %w", err)
	}
	fileSize := stat.Size()

	if flushedPos >= fileSize {
		slog.Info("nothing to compact", "flushedPos", flushedPos, "fileSize", fileSize)
		return f, nil
	}

	// Read unprocessed data (from flushedPos to EOF)
	if _, err := f.Seek(flushedPos, 0); err != nil {
		return f, fmt.Errorf("seek to flushedPos: %w", err)
	}
	remaining, err := io.ReadAll(f)
	if err != nil {
		return f, fmt.Errorf("read remaining: %w", err)
	}

	slog.Info("compacting file", "path", filePath, "fileSize", fileSize, "remaining", len(remaining), "flushedPos", flushedPos)

	tmpPath := filePath + ".compact.tmp"
	tmpFile, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return f, fmt.Errorf("create temp file: %w", err)
	}
	if _, err := tmpFile.Write(remaining); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return f, fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return f, fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return f, fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
		return f, fmt.Errorf("rename temp file over original: %w", err)
	}

	// f now points at the same unlinked, stale content rsyslog's own handle
	// does - it must be reopened too, not just rsyslog's.
	f.Close()
	newF, err := os.OpenFile(filePath, os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("reopen compacted file: %w", err)
	}

	if reopenLogFile != nil {
		if err := reopenLogFile(); err != nil {
			// Not fatal to compaction itself - the file on disk is correct
			// either way, and this function's own reader is already fine
			// (newF, above) - but rsyslog specifically won't see it until it
			// reopens on its own (process restart) or a later compaction's
			// call succeeds. Loud on purpose: a persistent failure here
			// means new lines rsyslog writes are being silently lost.
			slog.Error("compaction: failed to ask rsyslog to reopen the compacted log file - it may keep writing into the old, now-unlinked file until it restarts or a later compaction succeeds", "error", err)
		}
	}

	slog.Info("compaction done", "remaining", len(remaining))
	return newF, nil
}

const insertColumns = 13

// insertQueryColumns lists the syslog_logs columns in the order rowArgs
// produces values for; shared by the bulk and per-row insert paths so they
// can never drift apart.
const insertQueryColumns = `timestamp, hostname, fromhost_ip, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, matched_parsers, via_relay`

// maxInsertRows caps how many entries go into a single multi-row INSERT.
// 13 columns * 500 rows = 6500 placeholders, comfortably under Postgres's
// 65535-parameter-per-statement limit while still collapsing hundreds of
// round trips into one.
const maxInsertRows = 500

func rowArgs(entry model.IngestEntry) []interface{} {
	ts, err := parseTimestamp(entry.Timestamp)
	if err != nil {
		ts = time.Now()
	}
	parsedFields := json.RawMessage("{}")
	if len(entry.ParsedFields) > 0 {
		parsedFields = entry.ParsedFields
	}

	return []interface{}{
		ts, entry.Hostname, nullStr(entry.FromHostIP), nullStr(entry.AppName),
		nullStr(entry.ProcessID), nullStr(entry.MsgID), entry.Severity,
		nullStr(entry.Facility), entry.Message, nullStr(entry.RawMessage),
		parsedFields, pq.StringArray(entry.MatchedParsers), nullStr(entry.ViaRelay),
	}
}

// flushBatch inserts entries in chunks of up to maxInsertRows, each chunk as
// a single multi-row INSERT (one round trip instead of one per row - at high
// ingestion rates the per-Exec network round trip, not Postgres itself, was
// the bottleneck). If a chunk's bulk insert fails - e.g. one row in it has
// data Postgres rejects - it falls back to inserting that chunk's rows one
// at a time (see insertRowsIndividually) so a single bad row still can't
// take out the rest of the chunk with it.
func flushBatch(db *sql.DB, entries []model.IngestEntry, rate sharedstate.RateCounter) error {
	if len(entries) == 0 {
		return nil
	}

	ingested := 0
	for start := 0; start < len(entries); start += maxInsertRows {
		end := start + maxInsertRows
		if end > len(entries) {
			end = len(entries)
		}
		chunk := entries[start:end]

		n, err := insertChunk(db, chunk)
		if err != nil {
			slog.Warn("bulk insert failed, falling back to per-row insert", "rows", len(chunk), "error", err)
			n = insertRowsIndividually(db, chunk)
		}
		ingested += n
	}

	if ingested > 0 {
		slog.Info("flushed logs", "count", ingested)
		rate.Incr(ingested)
	}

	return nil
}

func insertChunk(db *sql.DB, entries []model.IngestEntry) (int, error) {
	var sb strings.Builder
	sb.WriteString("INSERT INTO syslog_logs (")
	sb.WriteString(insertQueryColumns)
	sb.WriteString(") VALUES ")

	args := make([]interface{}, 0, len(entries)*insertColumns)
	for i, entry := range entries {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteByte('(')
		base := i * insertColumns
		for c := 1; c <= insertColumns; c++ {
			if c > 1 {
				sb.WriteByte(',')
			}
			sb.WriteByte('$')
			sb.WriteString(strconv.Itoa(base + c))
		}
		sb.WriteByte(')')
		args = append(args, rowArgs(entry)...)
	}

	res, err := db.Exec(sb.String(), args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// insertRowsIndividually inserts entries one at a time, each its own
// implicit transaction (no shared batch transaction). A prior version
// wrapped the whole batch in one transaction with a SAVEPOINT around each
// row so a bad row could be rolled back without losing the rest - in
// practice, once one row aborted the transaction, ROLLBACK TO SAVEPOINT did
// not reliably return the connection to a usable state (subsequent rows
// kept failing with "current transaction is aborted" even though they were
// individually fine), so a single Postgres-rejected row could still take
// down the whole chunk. Committing independently per row makes that
// structurally impossible: nothing about one row's failure can touch any
// other row's outcome. This path only runs on the rare chunk whose bulk
// insert failed, so its lower throughput doesn't matter in practice.
func insertRowsIndividually(db *sql.DB, entries []model.IngestEntry) int {
	query := `INSERT INTO syslog_logs (` + insertQueryColumns + `)
		          VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`
	stmt, err := db.Prepare(query)
	if err != nil {
		slog.Error("prepare error", "error", err)
		return 0
	}
	defer stmt.Close()

	ingested := 0
	for _, entry := range entries {
		if _, err := stmt.Exec(rowArgs(entry)...); err != nil {
			slog.Error("insert error", "error", err)
			continue
		}
		ingested++
	}
	return ingested
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

// posFingerprintWindow is how many bytes immediately before a saved position
// get hashed into that position's fingerprint (see positionFingerprint) -
// large enough to make an accidental hash collision against unrelated
// content practically impossible, small enough that computing it on every
// save/load is unnoticeable next to the read/write work already happening
// around it.
const posFingerprintWindow = 256

// positionFingerprint hashes the bytes immediately before pos in filePath, so
// a saved position can be verified against the file's actual current content
// before being trusted - not just checked against the file's current size.
// compactFile rewrites the shared log file in place (truncate, then write
// the still-unflushed tail back); a crash between those two steps (Swarm
// killing the container on a node failure or a rolling update, see the
// stop_grace_period comment on the api service in docker-stack.app.yml)
// leaves it truncated or partially rewritten. If rsyslog - a separate,
// uncoordinated process/container still appending to the same file over NFS,
// see rsyslog/syslog.conf's omfile action - keeps growing it afterward, the
// file can coincidentally grow back past the stale saved position, which
// would let the old "pos <= file size" check alone wrongly trust it even
// though the content actually there now is completely different. Position 0
// has nothing before it, so it's always trivially valid regardless of
// content - loadStartPosition's DB fallback already treats "start of file"
// as always safe.
func positionFingerprint(filePath string, pos int64) (fingerprint string, ok bool) {
	if pos <= 0 {
		return "", true
	}
	f, err := os.Open(filePath)
	if err != nil {
		return "", false
	}
	defer f.Close()
	start := pos - posFingerprintWindow
	if start < 0 {
		start = 0
	}
	buf := make([]byte, pos-start)
	if _, err := f.ReadAt(buf, start); err != nil {
		return "", false
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), true
}

func savePosition(path string, pos int64, filePath string) {
	fp, ok := positionFingerprint(filePath, pos)
	if !ok {
		slog.Warn("could not fingerprint position for save, next restart will fall back to DB if needed", "pos", pos)
	}
	if err := os.WriteFile(path, []byte(strconv.FormatInt(pos, 10)+":"+fp), 0644); err != nil {
		slog.Error("save position error", "error", err)
	}
}

// loadPosition parses the "pos:fingerprint" format savePosition writes.
// ok is false both when the file is missing/malformed and when it's in the
// old bare-integer format written before fingerprinting existed (no ':') -
// either way there's no fingerprint to verify the position against, so the
// caller can't safely trust it and falls back to the DB-based scan instead.
// That fallback only costs a slower single startup right after upgrading to
// this format; every restart after that reads the new format.
func loadPosition(path string) (pos int64, fingerprint string, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, "", false
	}
	raw := strings.TrimSpace(string(data))
	sep := strings.IndexByte(raw, ':')
	if sep < 0 {
		return 0, "", false
	}
	pos, err = strconv.ParseInt(raw[:sep], 10, 64)
	if err != nil {
		return 0, "", false
	}
	return pos, raw[sep+1:], true
}

func loadStartPosition(db *sql.DB, filePath, posFile string) (filePos, flushedPos int64) {
	if pos, fp, ok := loadPosition(posFile); ok {
		if f, err := os.Open(filePath); err == nil {
			stat, _ := f.Stat()
			f.Close()
			if pos <= stat.Size() {
				if curFp, fpOK := positionFingerprint(filePath, pos); fpOK && curFp == fp {
					slog.Info("restored position from file", "pos", pos)
					return pos, pos
				}
				slog.Warn("saved position's content no longer matches the file (likely an interrupted compaction), falling back to DB", "pos", pos)
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
