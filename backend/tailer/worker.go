package tailer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"time"

	"database/sql"

	"logmara/alertengine"
	"logmara/model"
	"logmara/parser"
	"logmara/sharedstate"
)

const (
	workerBatchSize     = 5000
	workerBatchInterval = 500 * time.Millisecond
	defaultWorkerCount  = 0 // 0 = auto (NumCPU/2)
)

// WorkerPool manages a pool of workers that consume messages from the
// RabbitMQ queue, parse them, insert into DB, evaluate alerts, and
// report flush progress.
type WorkerPool struct {
	workers []*worker
	wg      sync.WaitGroup
	cancel  context.CancelFunc
}

// NumWorkers returns the number of workers in the pool.
func (wp *WorkerPool) NumWorkers() int {
	return len(wp.workers)
}

type worker struct {
	id        int
	parser    *parser.Engine
	db        *sql.DB
	alerts    *alertengine.Engine
	rate      sharedstate.RateCounter
	flushTrk  *sharedstate.FlushTracker
	queue     *sharedstate.Queue
	metrics   *WorkerMetrics
}

// WorkerMetrics tracks per-worker statistics.
type WorkerMetrics struct {
	ID            int
	Mutex         sync.RWMutex
	MsgsProcessed int64
	ParseErrors   int64
	DbInserts     int64
	LastFlushAt   time.Time
}

// WorkerMetricsPublic is a JSON-serializable snapshot of WorkerMetrics.
type WorkerMetricsPublic struct {
	ID            int       `json:"id"`
	MsgsProcessed int64     `json:"msgs_processed"`
	ParseErrors   int64     `json:"parse_errors"`
	DbInserts     int64     `json:"db_inserts"`
	LastFlushAt   time.Time `json:"last_flush_at"`
}

func NewWorkerPool(numWorkers int, db *sql.DB, alerts *alertengine.Engine,
	rate sharedstate.RateCounter, flushTrk *sharedstate.FlushTracker,
	queue *sharedstate.Queue) *WorkerPool {

	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
		if numWorkers < 2 {
			numWorkers = 2
		}
	}

	pool := &WorkerPool{}
	for i := 0; i < numWorkers; i++ {
		pe := parser.NewEngine(db)
		w := &worker{
			id:       i,
			parser:   pe,
			db:       db,
			alerts:   alerts,
			rate:     rate,
			flushTrk: flushTrk,
			queue:    queue,
			metrics: &WorkerMetrics{
				ID:    i,
				LastFlushAt: time.Now(),
			},
		}
		pool.workers = append(pool.workers, w)
	}
	return pool
}

func (wp *WorkerPool) Start(ctx context.Context) {
	workerCtx, cancel := context.WithCancel(ctx)
	wp.cancel = cancel

	for _, w := range wp.workers {
		wp.wg.Add(1)
		go func(worker *worker) {
			defer wp.wg.Done()
			worker.run(workerCtx)
		}(w)
	}
	slog.Info("worker pool started", "workers", len(wp.workers))
}

func (wp *WorkerPool) Wait() {
	wp.wg.Wait()
}

func (wp *WorkerPool) Cancel() {
	if wp.cancel != nil {
		wp.cancel()
	}
}

func (wp *WorkerPool) GetMetrics() []WorkerMetrics {
	metrics := make([]WorkerMetrics, len(wp.workers))
	for i, w := range wp.workers {
		w.metrics.Mutex.RLock()
		metrics[i] = *w.metrics
		w.metrics.Mutex.RUnlock()
	}
	return metrics
}

// GetPublicMetrics returns a JSON-serializable snapshot of per-worker metrics.
func (wp *WorkerPool) GetPublicMetrics() []WorkerMetricsPublic {
	metrics := make([]WorkerMetricsPublic, len(wp.workers))
	for i, w := range wp.workers {
		w.metrics.Mutex.RLock()
		metrics[i] = WorkerMetricsPublic{
			ID:            w.metrics.ID,
			MsgsProcessed: w.metrics.MsgsProcessed,
			ParseErrors:   w.metrics.ParseErrors,
			DbInserts:     w.metrics.DbInserts,
			LastFlushAt:   w.metrics.LastFlushAt,
		}
		w.metrics.Mutex.RUnlock()
	}
	return metrics
}

func (w *worker) run(ctx context.Context) {
	deliveries, err := w.queue.Consume(ctx)
	if err != nil {
		slog.Error("worker: failed to start consuming", "id", w.id, "error", err)
		return
	}

	slog.Info("worker started", "id", w.id)
	var entries []model.IngestEntry
	var queueEntries []sharedstate.QueueEntry
	lastFlush := time.Now()

	for {
		select {
		case <-ctx.Done():
			slog.Info("worker stopping", "id", w.id)
			return
		case delivery, ok := <-deliveries:
			if !ok {
				slog.Warn("worker: delivery channel closed", "id", w.id)
				return
			}

			var qe sharedstate.QueueEntry
			if err := json.Unmarshal(delivery.Body, &qe); err != nil {
				slog.Error("worker: unmarshal queue entry error", "id", w.id, "error", err)
				delivery.Ack(false)
				w.metrics.Mutex.Lock()
				w.metrics.ParseErrors++
				w.metrics.Mutex.Unlock()
				continue
			}

			line := qe.Line
			if line == "" {
				delivery.Ack(false)
				continue
			}

			var entry model.IngestEntry
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				slog.Error("worker: invalid JSON", "id", w.id, "error", err)
				sanitizedLine := sanitizeForPostgres(line)
				entry = model.IngestEntry{
					Timestamp: time.Now().Format(time.RFC3339),
					Hostname:  "unknown",
					Severity:  "error",
					Message:   fmt.Sprintf("[MALFORMED JSON] %s", sanitizedLine),
				}
				w.alerts.EvaluateMalformedJSON(w.db, sanitizedLine)
				w.metrics.Mutex.Lock()
				w.metrics.ParseErrors++
				w.metrics.Mutex.Unlock()
			}

			// Sanitize all fields
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
				delivery.Ack(false)
				continue
			}

			// Fix rsyslog mis-parse
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

			// Parse
			appName := entry.AppName
			result := w.parser.Parse(entry.Hostname, appName, entry.Message)
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
			queueEntries = append(queueEntries, qe)

			now := time.Now()
			if len(entries) >= workerBatchSize || now.Sub(lastFlush) >= workerBatchInterval {
				if err := flushBatch(w.db, entries, w.rate); err != nil {
					slog.Error("worker: flush error", "id", w.id, "error", err)
				} else {
					w.alerts.EvaluateBatch(w.db, entries)
					w.flushTrk.ReportFlushed(ctx, queueEntries)
					w.metrics.Mutex.Lock()
					w.metrics.DbInserts += int64(len(entries))
					w.metrics.LastFlushAt = now
					w.metrics.Mutex.Unlock()
				}
				entries = entries[:0]
				queueEntries = queueEntries[:0]
				lastFlush = now
			}

			delivery.Ack(false)
			w.metrics.Mutex.Lock()
			w.metrics.MsgsProcessed++
			w.metrics.Mutex.Unlock()
		}
	}
}
