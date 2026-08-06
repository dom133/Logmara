package tailer

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"logmara/sharedstate"
)

const tailerMetricsRedisKey = "tailer:metrics"
const tailerMetricsTTL = 10 * time.Second

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

// tailerMetricsPublic is a JSON-serializable snapshot of TailerMetrics
// stored in Redis so non-leader replicas can serve the admin endpoint.
type tailerMetricsPublic struct {
	NumWorkers    int                   `json:"num_workers"`
	QueueDepth    int64                 `json:"queue_depth"`
	FlushedPos    int64                 `json:"flushed_pos"`
	FlushedSeq    int64                 `json:"flushed_seq"`
	LogsPerSec    float64               `json:"logs_per_sec"`
	WorkerMetrics []WorkerMetricsPublic `json:"worker_metrics"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

// TailerMetricsCollector periodically updates TailerMetrics from live sources.
type TailerMetricsCollector struct {
	metrics  *TailerMetrics
	queue    *sharedstate.Queue
	flushTrk *sharedstate.FlushTracker
	rate     sharedstate.RateCounter
	pool     *WorkerPool
	client   *sharedstate.Client
	stop     chan struct{}
}

func NewTailerMetricsCollector(queue *sharedstate.Queue, flushTrk *sharedstate.FlushTracker,
	rate sharedstate.RateCounter, pool *WorkerPool, client *sharedstate.Client) *TailerMetricsCollector {

	return &TailerMetricsCollector{
		metrics:  &TailerMetrics{},
		queue:    queue,
		flushTrk: flushTrk,
		rate:     rate,
		pool:     pool,
		client:   client,
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

	m := tailerMetricsPublic{
		NumWorkers:    c.pool.NumWorkers(),
		QueueDepth:    queueDepth,
		FlushedPos:    flushedPos,
		FlushedSeq:    flushedSeq,
		LogsPerSec:    logsPerSec,
		WorkerMetrics: c.pool.GetPublicMetrics(),
		UpdatedAt:     time.Now(),
	}

	c.metrics.mu.Lock()
	c.metrics.QueueDepth = queueDepth
	c.metrics.FlushedPos = flushedPos
	c.metrics.FlushedSeq = flushedSeq
	c.metrics.LogsPerSec = logsPerSec
	c.metrics.WorkerMetrics = workerMetrics
	c.metrics.UpdatedAt = time.Now()
	c.metrics.mu.Unlock()

	// Persist to Redis so non-leader replicas can serve the admin endpoint
	if c.client != nil {
		data, err := json.Marshal(m)
		if err == nil {
			c.client.Raw().Set(ctx, tailerMetricsRedisKey, data, tailerMetricsTTL)
		}
	}
}
