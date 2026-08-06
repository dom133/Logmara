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
	"logmara/util"
)

// sanitizeForPostgres repairs invalid UTF-8 byte sequences and strips
// embedded NUL bytes so Postgres's text/jsonb storage won't reject them.
func sanitizeForPostgres(s string) string {
	if s == "" {
		return s
	}
	if !strings.ContainsRune(s, 0) && utf8.ValidString(s) {
		return s
	}
	return strings.ReplaceAll(strings.ToValidUTF8(s, "�"), "\x00", "")
}

var truncatedISORe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}$`)

const (
	compactionInterval = 30 * time.Minute
	maxFileSize        = 100 * 1024 * 1024
	positionFileName   = ".tailer_pos"
	maxLineSize        = 10 * 1024 * 1024
)

// lineSplitter behaves like bufio.ScanLines, with two differences:
//   - force-cuts at maxLineSize instead of growing forever
//   - never force-emits a trailing chunk at atEOF
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

const vipMarkerPath = "/data/.vip_master"
const myNodeEnvKey = "MY_NODE"
const vipCheckInterval = 5 * time.Second
const vipStartupDelay = 3 * time.Second
const vipStartupJitterMax = 2 * time.Second

// currentMetrics holds a reference to the active TailerMetricsCollector so
// the admin API can expose live pipeline stats. It's nil when no pipeline
// is running (single-server mode or RabbitMQ unavailable).
var currentMetrics *TailerMetricsCollector

// currentPipeline holds a reference to the active pipeline so the admin API
// can purge the RabbitMQ queue on demand.
var currentPipeline *pipeline

// currentSharedClient is the shared Redis client (nil in single-server mode).
// Used by GetTailerMetrics to read pipeline metrics from Redis so that
// non-leader replicas can still serve the admin endpoint.
var currentSharedClient *sharedstate.Client

// GetTailerMetrics returns the latest snapshot of tailer pipeline metrics.
// On the VIP leader it reads from the local metrics collector. On a
// non-leader replica it reads the snapshot from Redis (written by the
// leader every 2 s). Returns nil when no pipeline is active.
func GetTailerMetrics() *TailerMetrics {
	if currentMetrics != nil {
		m := currentMetrics.Get()
		return &m
	}
	if currentSharedClient != nil {
		return readMetricsFromRedis()
	}
	return nil
}

func readMetricsFromRedis() *TailerMetrics {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	data, err := currentSharedClient.Raw().Get(ctx, tailerMetricsRedisKey).Bytes()
	if err != nil || len(data) == 0 {
		return nil
	}
	var m tailerMetricsPublic
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return &TailerMetrics{
		NumWorkers:    m.NumWorkers,
		QueueDepth:    m.QueueDepth,
		FlushedPos:    m.FlushedPos,
		FlushedSeq:    m.FlushedSeq,
		LogsPerSec:    m.LogsPerSec,
		WorkerMetrics: metricsPublicToWorkerMetrics(m.WorkerMetrics),
		UpdatedAt:     m.UpdatedAt,
	}
}

func metricsPublicToWorkerMetrics(pub []WorkerMetricsPublic) []WorkerMetrics {
	metrics := make([]WorkerMetrics, len(pub))
	for i, p := range pub {
		metrics[i] = WorkerMetrics{
			ID:            p.ID,
			MsgsProcessed: p.MsgsProcessed,
			ParseErrors:   p.ParseErrors,
			DbInserts:     p.DbInserts,
			LastFlushAt:   p.LastFlushAt,
		}
	}
	return metrics
}

// PurgeTailerQueue purges the RabbitMQ ingestion queue and resets the
// flush tracker. Returns the number of messages removed from the queue.
func PurgeTailerQueue() uint32 {
	if currentPipeline == nil || currentPipeline.queue == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msgs, err := currentPipeline.queue.Purge(ctx)
	if err != nil {
		slog.Error("tailer: failed to purge queue", "error", err)
		return 0
	}
	if currentPipeline.flushTrk != nil {
		currentPipeline.flushTrk.Reset(ctx)
		slog.Info("tailer: flush tracker reset")
	}
	return msgs
}

// ResetTailerPosition clears the tailer position checkpoint in Redis.
func ResetTailerPosition() {
	if currentSharedClient != nil {
		currentSharedClient.ResetTailerPosition()
		slog.Info("tailer: Redis position reset")
	}
}

// pipeline holds the components of the distributed tailer pipeline.
type pipeline struct {
	queue       *sharedstate.Queue
	flushTrk    *sharedstate.FlushTracker
	workerPool  *WorkerPool
	metricsColl *TailerMetricsCollector
}

// Run starts the log tailer. When sharedClient is nil (single-server/
// single-replica deployments), it runs the ingestion loop directly.
// When sharedClient is set (multiple api replicas over NFS), it uses
// the distributed pipeline with RabbitMQ.
func Run(ctx context.Context, db *sql.DB, filePath string, engine *parser.Engine, ic control.IngestionController, alerts *alertengine.Engine, rate sharedstate.RateCounter, reopenLogFile func() error, sharedClient *sharedstate.Client) {
	if sharedClient == nil {
		runIngestionLoop(ctx, db, filePath, engine, ic, alerts, rate, reopenLogFile, nil)
		return
	}
	currentSharedClient = sharedClient
	runWithVIPElection(ctx, db, filePath, engine, ic, alerts, rate, reopenLogFile, sharedClient)
}

func runWithVIPElection(ctx context.Context, db *sql.DB, filePath string, engine *parser.Engine, ic control.IngestionController, alerts *alertengine.Engine, rate sharedstate.RateCounter, reopenLogFile func() error, sharedClient *sharedstate.Client) {
	myNode := strings.TrimSpace(os.Getenv(myNodeEnvKey))
	if myNode == "" {
		slog.Warn("tailer: MY_NODE not set, falling back to os.Hostname()")
		myNode, _ = os.Hostname()
	}

	startupJitter := time.Duration(rand.Int63n(int64(vipStartupJitterMax)))
	if !sleepOrDone(ctx, vipStartupDelay+startupJitter) {
		return
	}

	for {
		if ctx.Err() != nil {
			return
		}

		markerNode, err := readMarkerNode()
		if err != nil || markerNode != myNode {
			if !sleepOrDone(ctx, vipCheckInterval) {
				return
			}
			continue
		}

		slog.Info("tailer: VIP marker matches this node, starting ingestion", "my_node", myNode)
		leaderCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})

		// Start the distributed pipeline
		pipeline := startPipeline(leaderCtx, db, engine, ic, alerts, rate, filePath, sharedClient)

		if pipeline.queue != nil && pipeline.flushTrk != nil {
			go func() {
				defer close(done)
				// Run the file reader on the VIP leader
				FileReader(leaderCtx, filePath, pipeline.queue, pipeline.flushTrk, ic, reopenLogFile, sharedClient)
			}()
		} else {
			// RabbitMQ unavailable — runIngestionLoop was started inside
			// startPipeline as a fallback.  Keep this goroutine alive so
			// the VIP watchdog below can trigger graceful shutdown.
			go func() {
				defer close(done)
				<-leaderCtx.Done()
			}()
		}

		// Periodic save position from flushed progress
		go func() {
			posFile := filepath.Join(filepath.Dir(filePath), positionFileName)
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-leaderCtx.Done():
					return
				case <-ticker.C:
					_, flushedPos := pipeline.flushTrk.GetFlushedPos(leaderCtx)
					if flushedPos > 0 {
						savePosition(posFile, flushedPos, filePath, sharedClient)
					}
				}
			}
		}()

		// While tailing, check that VIP marker still matches
	vipLoop:
		for {
			select {
			case <-ctx.Done():
				cancel()
				<-done
				stopPipeline(pipeline)
				return
			case <-time.After(vipCheckInterval):
				markerNode, err := readMarkerNode()
				if err != nil || markerNode != myNode {
					slog.Warn("tailer: VIP marker no longer matches, stepping down", "my_node", myNode, "marker_node", markerNode)
					cancel()
					<-done
					stopPipeline(pipeline)
					break vipLoop
				}
			}
		}
	}
}

func startPipeline(ctx context.Context, db *sql.DB, engine *parser.Engine, ic control.IngestionController,
	alerts *alertengine.Engine, rate sharedstate.RateCounter, filePath string, sharedClient *sharedstate.Client) *pipeline {

	rabbitmqURL := util.ResolveRabbitMQURL()
	if rabbitmqURL == "" {
		rabbitmqURL = "amqp://logmara:logmara@localhost:5672"
	}

	queue, err := sharedstate.NewQueue(rabbitmqURL)
	if err != nil {
		slog.Error("tailer: failed to connect to RabbitMQ, falling back to local ingestion", "error", err)
		// Fallback to local ingestion loop
		go func() {
			runIngestionLoop(ctx, db, filePath, engine, ic, alerts, rate, nil, sharedClient)
		}()
		return &pipeline{}
	}

	flushTrk := sharedstate.NewFlushTracker(sharedClient)
	workerPool := NewWorkerPool(0, db, alerts, rate, flushTrk, queue)
	metricsColl := NewTailerMetricsCollector(queue, flushTrk, rate, workerPool, sharedClient)

	workerPool.Start(ctx)
	metricsColl.Start(ctx)
	currentMetrics = metricsColl

	slog.Info("tailer: pipeline started", "rabbitmq", rabbitmqURL)
	p := &pipeline{
		queue:       queue,
		flushTrk:    flushTrk,
		workerPool:  workerPool,
		metricsColl: metricsColl,
	}
	currentPipeline = p
	return p
}

func stopPipeline(p *pipeline) {
	if p == nil {
		return
	}
	slog.Info("tailer: stopping pipeline")
	if p.metricsColl != nil {
		p.metricsColl.Stop()
		currentMetrics = nil
	}
	currentPipeline = nil
	if p.workerPool != nil {
		p.workerPool.Cancel()
		p.workerPool.Wait()
	}
	if p.queue != nil {
		if err := p.queue.Close(); err != nil {
			slog.Warn("tailer: queue close error", "error", err)
		}
	}
	slog.Info("tailer: pipeline stopped")
}

func readMarkerNode() (string, error) {
	data, err := os.ReadFile(vipMarkerPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func sleepOrDone(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func runIngestionLoop(ctx context.Context, db *sql.DB, filePath string, engine *parser.Engine, ic control.IngestionController, alerts *alertengine.Engine, rate sharedstate.RateCounter, reopenLogFile func() error, sharedClient *sharedstate.Client) {
	slog.Info("file tailer started", "path", filePath)
	batchSize := 5000
	batchInterval := 500 * time.Millisecond

	posFile := filepath.Join(filepath.Dir(filePath), positionFileName)
	filePos, flushedPos := loadStartPosition(db, filePath, posFile, sharedClient)

	var entries []model.IngestEntry
	lastFlush := time.Now()
	lastCompaction := time.Now()
	batchStartPos := int64(0)

	defer func() {
		if len(entries) > 0 {
			slog.Info("flushing remaining entries on shutdown", "count", len(entries))
			if err := flushBatch(db, entries, rate); err != nil {
				slog.Error("final flush error", "error", err)
			} else {
				flushedPos = batchStartPos
				savePosition(posFile, flushedPos, filePath, sharedClient)
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

		shouldCompact := time.Since(lastCompaction) > compactionInterval || fileSize > maxFileSize
		enoughFlushed := fileSize > 0 && flushedPos > 0 && (fileSize-flushedPos) < fileSize/4
		if shouldCompact && enoughFlushed {
			newF, err := compactFile(f, flushedPos, filePath, reopenLogFile)
			if err != nil {
				slog.Error("compaction error", "error", err)
				f.Close()
				if !sleepOrDone(ctx, 1*time.Second) {
					return
				}
				continue
			}
			f = newF
			filePos = 0
			flushedPos = 0
			savePosition(posFile, 0, filePath, sharedClient)
			lastCompaction = time.Now()
		}

		if _, err := f.Seek(filePos, 0); err != nil {
			f.Close()
			if !sleepOrDone(ctx, 1*time.Second) {
				return
			}
			continue
		}

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
				slog.Warn("tailer: position was mid-line, backseeking", "was", filePos, "now", seekPos)
				filePos = seekPos
				flushedPos = seekPos
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

			if truncatedISORe.MatchString(entry.AppName) {
				fullMsg := entry.AppName + ":" + entry.Message
				entry.Message = fullMsg
				entry.RawMessage = fullMsg
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
						savePosition(posFile, flushedPos, filePath, sharedClient)
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
			filePos = curFilePos
		}
		f.Close()

		if len(entries) > 0 && !ic.IsPaused() {
			if err := flushBatch(db, entries, rate); err != nil {
				slog.Error("flush error", "error", err)
			} else {
				flushedPos = batchStartPos
				savePosition(posFile, flushedPos, filePath, sharedClient)
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

const insertColumns = 13
const insertQueryColumns = `timestamp, hostname, fromhost_ip, app_name, process_id, msg_id, severity, facility, message, raw_message, parsed_fields, matched_parsers, via_relay`
const maxInsertRows = 5000

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

const posFingerprintWindow = 256

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

func savePosition(path string, pos int64, filePath string, sharedClient *sharedstate.Client) {
	fp, ok := positionFingerprint(filePath, pos)
	if !ok {
		slog.Warn("could not fingerprint position for save", "pos", pos)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(pos, 10)+":"+fp), 0644); err != nil {
		slog.Error("save position error", "error", err)
		return
	}
	if sf, err := os.OpenFile(tmp, os.O_WRONLY, 0644); err == nil {
		sf.Sync()
		sf.Close()
	}
	if err := os.Rename(tmp, path); err != nil {
		slog.Error("save position rename error", "error", err)
		os.Remove(tmp)
		return
	}
	if sharedClient != nil {
		sharedClient.SaveTailerPosition(pos, fp)
	}
}

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

func loadStartPosition(db *sql.DB, filePath, posFile string, sharedClient *sharedstate.Client) (filePos, flushedPos int64) {
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

	f.Close()
	newF, err := os.OpenFile(filePath, os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("reopen compacted file: %w", err)
	}

	if reopenLogFile != nil {
		if err := reopenLogFile(); err != nil {
			slog.Error("compaction: failed to ask rsyslog to reopen", "error", err)
		}
	}

	slog.Info("compaction done", "remaining", len(remaining))
	return newF, nil
}
