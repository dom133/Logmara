package tailer

import (
	"context"
	"sync"
	"time"

	"logmara/sharedstate"
)

// TailerMetrics aggregates global and per-worker tailer metrics.
type TailerMetrics struct {
	mu             sync.RWMutex
	NumWorkers     int
	QueueDepth     int64
	FlushedPos     int64
	FlushedSeq     int64
	LogsPerSec     float64
	WorkerMetrics  []WorkerMetrics
	UpdatedAt      time.Time
}

// TailerMetricsCollector periodically updates TailerMetrics from live sources.
type TailerMetricsCollector struct {
	metrics  *TailerMetrics
	queue    *sharedstate.Queue
	flushTrk *sharedstate.FlushTracker
	rate     sharedstate.RateCounter
	pool     *WorkerPool
	stop     chan struct{}
}

func NewTailerMetricsCollector(queue *sharedstate.Queue, flushTrk *sharedstate.FlushTracker,
	rate sharedstate.RateCounter, pool *WorkerPool) *TailerMetricsCollector {

	return &TailerMetricsCollector{
		metrics:  &TailerMetrics{},
		queue:    queue,
		flushTrk: flushTrk,
		rate:     rate,
		pool:     pool,
		stop:     make(chan struct{}),
	}
}

func (c *TailerMetricsCollector) Start(ctx context.Context) {
	go c.updateLoop(ctx)
}

func (c *TailerMetricsCollector) Stop() {
	close(c.stop)
}

func (c *TailerMetricsCollector) Get() TailerMetrics {
	c.metrics.mu.RLock()
	defer c.metrics.mu.RUnlock()
	return *c.metrics
}

func (c *TailerMetricsCollector) updateLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stop:
			return
		case <-ticker.C:
			c.update(ctx)
		}
	}
}

func (c *TailerMetricsCollector) update(ctx context.Context) {
	queueDepth := c.queue.Len(ctx)
	flushedSeq, flushedPos := c.flushTrk.GetFlushedPos(ctx)
	logsPerSec := c.rate.Rate(ctx, 60)

	workerMetrics := c.pool.GetMetrics()

	c.metrics.mu.Lock()
	c.metrics.QueueDepth = queueDepth
	c.metrics.FlushedPos = flushedPos
	c.metrics.FlushedSeq = flushedSeq
	c.metrics.LogsPerSec = logsPerSec
	c.metrics.WorkerMetrics = workerMetrics
	c.metrics.UpdatedAt = time.Now()
	c.metrics.mu.Unlock()
}
