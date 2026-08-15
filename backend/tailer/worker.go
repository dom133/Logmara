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



	amqp "github.com/rabbitmq/amqp091-go"

	"logmara/alertengine"
	"logmara/control"
	"logmara/db"
	"logmara/model"
	"logmara/parser"
	"logmara/sharedstate"
)

const (
	workerBatchSize        = 5000
	workerBatchInterval    = 500 * time.Millisecond
	defaultWorkerCount     = 0 // 0 = auto (NumCPU/2)
	workerReconnectDelay   = 5 * time.Second
	workerReconnectMaxLog  = 10
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
	id         int
	parser     *parser.Engine
	pool       *db.DynamicPool
	alerts     *alertengine.Engine
	rate       sharedstate.RateCounter
	flushTrk   *sharedstate.FlushTracker
	queue      *sharedstate.Queue
	ic         control.IngestionController
	metrics    *WorkerMetrics
}

// WorkerMetrics tracks per-worker statistics.
type WorkerMetrics struct {
	ID             int
	Mutex          sync.RWMutex
	MsgsProcessed  int64
	ParseErrors    int64
	DbInserts      int64
	LastFlushAt    time.Time
	ReconnectCount int64 // lifetime count of RabbitMQ reconnect attempts, for spotting flaky brokers
}

// WorkerMetricsPublic is a JSON-serializable snapshot of WorkerMetrics.
type WorkerMetricsPublic struct {
	ID             int       `json:"id"`
	NodeID         string    `json:"node_id"`
	MsgsProcessed  int64     `json:"msgs_processed"`
	ParseErrors    int64     `json:"parse_errors"`
	DbInserts      int64     `json:"db_inserts"`
	LastFlushAt    time.Time `json:"last_flush_at"`
	ReconnectCount int64     `json:"reconnect_count"`
}

func NewWorkerPool(numWorkers int, pool *db.DynamicPool, alerts *alertengine.Engine,
	rate sharedstate.RateCounter, flushTrk *sharedstate.FlushTracker,
	queue *sharedstate.Queue, ic control.IngestionController) *WorkerPool {

	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
		if numWorkers < 2 {
			numWorkers = 2
		}
	}

	wp := &WorkerPool{}
	for i := 0; i < numWorkers; i++ {
		pe := parser.NewEngine(pool)
		w := &worker{
			id:       i,
			parser:   pe,
			pool:     pool,
			alerts:   alerts,
			rate:     rate,
			flushTrk: flushTrk,
			queue:    queue,
			ic:       ic,
			metrics: &WorkerMetrics{
				ID:    i,
				LastFlushAt: time.Now(),
			},
		}
		wp.workers = append(wp.workers, w)
	}
	return wp
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
		metrics[i] = WorkerMetrics{
			ID:             w.metrics.ID,
			MsgsProcessed:  w.metrics.MsgsProcessed,
			ParseErrors:    w.metrics.ParseErrors,
			DbInserts:      w.metrics.DbInserts,
			LastFlushAt:    w.metrics.LastFlushAt,
			ReconnectCount: w.metrics.ReconnectCount,
		}
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
			ID:             w.metrics.ID,
			MsgsProcessed:  w.metrics.MsgsProcessed,
			ParseErrors:    w.metrics.ParseErrors,
			DbInserts:      w.metrics.DbInserts,
			LastFlushAt:    w.metrics.LastFlushAt,
			ReconnectCount: w.metrics.ReconnectCount,
		}
		w.metrics.Mutex.RUnlock()
	}
	return metrics
}

func (w *worker) run(ctx context.Context) {
	reconnectAttempts := 0

	for {
		select {
		case <-ctx.Done():
			slog.Info("worker: shutting down (context canceled)", "id", w.id)
			return
		default:
		}

		if reconnectAttempts > 0 {
			logMsg := "worker: attempting to reconnect"
			if reconnectAttempts <= workerReconnectMaxLog {
				slog.Warn(logMsg, "id", w.id, "attempt", reconnectAttempts)
			} else if reconnectAttempts == workerReconnectMaxLog+1 {
				slog.Warn("worker: suppressing further reconnect logs (too many attempts)", "id", w.id, "attempt", reconnectAttempts)
			}
		}

		deliveriesChan, err := w.queue.Consume(ctx)
		if err != nil {
			slog.Error("worker: failed to start consuming, will retry", "id", w.id, "error", err)
			reconnectAttempts++
			w.metrics.Mutex.Lock()
			w.metrics.ReconnectCount++
			w.metrics.Mutex.Unlock()
			if !sleepOrDone(ctx, workerReconnectDelay) {
				return
			}
			continue
		}

		reconnectAttempts = 0
		slog.Info("worker: consume started successfully", "id", w.id)

		var entries []model.IngestEntry
		var queueEntries []sharedstate.QueueEntry
		var batchDelivries []amqp.Delivery
		lastFlush := time.Now()

		consumeLoop:
		for {
			select {
			case <-ctx.Done():
				slog.Info("worker: stopping (context canceled)", "id", w.id)
				return
			case delivery, ok := <-deliveriesChan:
				if !ok {
					slog.Warn("worker: delivery channel closed, reconnecting", "id", w.id)
					for _, d := range batchDelivries {
						d.Nack(false, true)
					}
					slog.Info("worker: requeued pending deliveries before reconnect", "id", w.id, "count", len(batchDelivries))
					break consumeLoop
				}

				if w.ic.IsPaused() {
					delivery.Nack(false, true)
					continue
				}

				var qe sharedstate.QueueEntry
				if err := json.Unmarshal(delivery.Body, &qe); err != nil {
					slog.Error("worker: unmarshal queue entry error", "id", w.id, "error", err)
					// The envelope itself is corrupt, so qe.Seq can't be
					// trusted - encoding/json gives no guarantee about which
					// fields got set before the error. Try a lenient decode
					// that only needs seq/next_pos (the "line" payload can
					// still be garbage) so a single corrupted message doesn't
					// permanently freeze the flush tracker's contiguous
					// sequence - a stuck seq means flushedPos/.tailer_pos
					// never advance again and logs.jsonl is never compacted.
					var partial struct {
						Seq     int64 `json:"seq"`
						NextPos int64 `json:"next_pos"`
					}
					if err2 := json.Unmarshal(delivery.Body, &partial); err2 == nil && partial.Seq > 0 {
						w.flushTrk.ReportFlushed(ctx, []sharedstate.QueueEntry{{Seq: partial.Seq, NextPos: partial.NextPos}})
					} else {
						slog.Error("worker: cannot recover seq from corrupted queue entry, flush tracker progress may stall here", "id", w.id)
					}
					delivery.Ack(false)
					w.metrics.Mutex.Lock()
					w.metrics.ParseErrors++
					w.metrics.MsgsProcessed++
					w.metrics.Mutex.Unlock()
					continue
				}

				line := qe.Line
				if line == "" {
					w.flushTrk.ReportFlushed(ctx, []sharedstate.QueueEntry{qe})
					delivery.Ack(false)
					w.metrics.Mutex.Lock()
					w.metrics.MsgsProcessed++
					w.metrics.Mutex.Unlock()
					continue
				}

				var entry model.IngestEntry
				if err := json.Unmarshal([]byte(line), &entry); err != nil {
					// Every valid line in logs.jsonl starts with "{" (JsonLines
					// template). A line that doesn't is a partial write from
					// rsyslog that made it into the queue — discard silently.
					if !strings.HasPrefix(line, "{") {
						w.flushTrk.ReportFlushed(ctx, []sharedstate.QueueEntry{qe})
						delivery.Ack(false)
						w.metrics.Mutex.Lock()
						w.metrics.MsgsProcessed++
						w.metrics.Mutex.Unlock()
						continue
					}

					debug := line
					if len(debug) > 200 {
						debug = debug[:200]
					}
					slog.Error("worker: invalid JSON", "id", w.id, "error", err, "debug", debug, "lineLen", len(line))
					sanitizedLine := sanitizeForPostgres(line)
					tag := "[MALFORMED JSON]"
					if looksTruncatedAtSource(line, err) {
						tag = "[TRUNCATED AT SOURCE]"
					}
					entry = model.IngestEntry{
						Timestamp: time.Now().Format(time.RFC3339),
						Hostname:  "unknown",
						Severity:  "error",
						Message:   fmt.Sprintf("%s %s", tag, sanitizedLine),
					}
					w.alerts.EvaluateMalformedJSON(sanitizedLine)
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
					w.flushTrk.ReportFlushed(ctx, []sharedstate.QueueEntry{qe})
					delivery.Ack(false)
					w.metrics.Mutex.Lock()
					w.metrics.MsgsProcessed++
					w.metrics.Mutex.Unlock()
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
				batchDelivries = append(batchDelivries, delivery)

				now := time.Now()
				if len(entries) >= workerBatchSize || now.Sub(lastFlush) >= workerBatchInterval {
					if w.ic != nil && w.ic.IsPaused() {
						for _, d := range batchDelivries {
							d.Nack(false, true)
						}
						entries = nil
						queueEntries = nil
						batchDelivries = nil
						continue
					}
					if err := flushBatch(w.pool.Get(), entries, w.rate); err != nil {
						slog.Error("worker: flush error", "id", w.id, "error", err)
						for _, d := range batchDelivries {
							d.Nack(false, true)
						}
					} else {
						w.alerts.EvaluateBatch(entries)
						w.flushTrk.ReportFlushed(ctx, queueEntries)
						for _, d := range batchDelivries {
							d.Ack(false)
						}
						w.metrics.Mutex.Lock()
						w.metrics.DbInserts += int64(len(entries))
						w.metrics.MsgsProcessed += int64(len(entries))
						w.metrics.LastFlushAt = now
						w.metrics.Mutex.Unlock()
					}
					entries = nil
					queueEntries = nil
					batchDelivries = nil
					lastFlush = now
				}
			}
		}

		slog.Warn("worker: consume loop exited, reconnecting", "id", w.id)
		reconnectAttempts++
		w.metrics.Mutex.Lock()
		w.metrics.ReconnectCount++
		w.metrics.Mutex.Unlock()
		if !sleepOrDone(ctx, workerReconnectDelay) {
			return
		}
	}
}
