package sharedstate

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	rabbitmqExchange = "" // default direct exchange
	rabbitmqQueue    = "tailer-ingestion"
	rabbitmqPrefetch = 1000
	rabbitmqMaxLen   = 50000
	rabbitmqReconnectInterval = 5 * time.Second
	// queueReconnectMaxLog caps how many consecutive failed-reconnect
	// attempts get logged at Warn, so a prolonged broker outage doesn't
	// flood the log with one line per attempt.
	queueReconnectMaxLog = 10
)

// queueLog tags every log line from this file with a component attribute so
// it's easy to filter connection-layer logs (shared by both the file-reader
// publisher and the RabbitMQ worker consumers) apart from either side's own
// logs.
var queueLog = slog.With("component", "rabbitmq-queue")

// Queue wraps a RabbitMQ connection, channel, and queue for the tailer
// ingestion pipeline. It provides publish/consume with auto-reconnect.
type Queue struct {
	mu       sync.RWMutex
	url      string
	conn     *amqp.Connection
	channel  *amqp.Channel
	queue    amqp.Queue
	closed   bool

	lastRotatedAt time.Time
	lastResult    string
	lastError     string

	// reconnectAttempts counts consecutive failed reconnect attempts since
	// the last successful connect, regardless of whether the triggering
	// call was a publish (file-reader) or a consume (worker). Reset to 0
	// on success.
	reconnectAttempts int
}

func NewQueue(url string) (*Queue, error) {
	q := &Queue{url: url}
	if err := q.connect(); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *Queue) connect() error {
	var err error
	q.conn, err = amqp.Dial(q.url)
	if err != nil {
		return err
	}
	q.channel, err = q.conn.Channel()
	if err != nil {
		q.conn.Close()
		return err
	}
	// Declare queue with durable=false (in-memory only, tailer data lives in the file)
	q.queue, err = q.channel.QueueDeclare(
		rabbitmqQueue,
		false, // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		q.channel.Close()
		q.conn.Close()
		return err
	}
	return nil
}

func (q *Queue) healthyLocked() bool {
	return q.conn != nil && !q.conn.IsClosed() && q.channel != nil && !q.channel.IsClosed()
}

func (q *Queue) ensureConnected() error {
	q.mu.RLock()
	if q.healthyLocked() {
		q.mu.RUnlock()
		return nil
	}
	q.mu.RUnlock()

	q.mu.Lock()
	defer q.mu.Unlock()
	if q.healthyLocked() {
		return nil
	}
	return q.reconnectLocked()
}

func (q *Queue) reconnectLocked() error {
	q.reconnectAttempts++
	attempt := q.reconnectAttempts
	if attempt <= queueReconnectMaxLog {
		queueLog.Warn("connection lost or closed, reconnecting", "attempt", attempt)
	} else if attempt == queueReconnectMaxLog+1 {
		queueLog.Warn("suppressing further reconnect logs (too many attempts)", "attempt", attempt)
	}

	if q.conn != nil {
		q.conn.Close()
	}
	if err := q.connect(); err != nil {
		if attempt <= queueReconnectMaxLog {
			queueLog.Error("reconnect to RabbitMQ failed, will retry", "attempt", attempt, "error", err)
		}
		return err
	}
	if attempt > 1 {
		queueLog.Info("reconnected to RabbitMQ successfully", "attempts", attempt)
	} else {
		queueLog.Info("reconnected to RabbitMQ successfully")
	}
	q.reconnectAttempts = 0
	return nil
}

// Publish sends a single message to the ingestion queue.
func (q *Queue) Publish(ctx context.Context, data []byte) error {
	if err := q.ensureConnected(); err != nil {
		return err
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.channel.PublishWithContext(
		ctx,
		rabbitmqExchange,
		rabbitmqQueue,
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        data,
		},
	)
}

// Consume starts consuming messages from the queue. Returns a channel that
// delivers messages to the caller. The caller must ACK/NACK each delivery.
func (q *Queue) Consume(ctx context.Context) (<-chan amqp.Delivery, error) {
	if err := q.ensureConnected(); err != nil {
		return nil, fmt.Errorf("queue consume: ensure connected failed: %w", err)
	}
	q.mu.RLock()
	defer q.mu.RUnlock()

	if !q.healthyLocked() {
		return nil, fmt.Errorf("queue consume: connection or channel closed unexpectedly after ensureConnected")
	}

	if err := q.channel.Qos(rabbitmqPrefetch, 0, false); err != nil {
		return nil, fmt.Errorf("queue consume: Qos failed: %w", err)
	}
	queueLog.Debug("starting consumer", "queue", rabbitmqQueue)
	return q.channel.Consume(
		rabbitmqQueue,
		"", // consumer tag (auto-generated)
		false, // auto-ack
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // args
	)
}

// IsClosed returns true if the RabbitMQ connection is closed or unavailable.
func (q *Queue) IsClosed() bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.conn == nil || q.conn.IsClosed()
}

// Len returns the approximate number of messages waiting in the queue.
func (q *Queue) Len(ctx context.Context) int64 {
	if err := q.ensureConnected(); err != nil {
		return 0
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	return int64(q.queue.Messages)
}

// IsFull returns true if the queue has reached maxLen messages (backpressure).
func (q *Queue) IsFull(ctx context.Context) bool {
	return q.Len(ctx) >= rabbitmqMaxLen
}

// MaxLen returns the queue's configured backpressure threshold, so callers
// that already have a Len() reading (e.g. the metrics collector) can derive
// fullness without a second round trip.
func (q *Queue) MaxLen() int64 {
	return rabbitmqMaxLen
}

// Purge removes all messages from the RabbitMQ queue. Returns the number
// of messages that were removed.
func (q *Queue) Purge(_ context.Context) (uint32, error) {
	if err := q.ensureConnected(); err != nil {
		return 0, err
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	msgs, err := q.channel.QueuePurge(
		rabbitmqQueue,
		false, // no-wait
	)
	if err != nil {
		return 0, err
	}
	queueLog.Info("queue purged", "messages_removed", msgs)
	return uint32(msgs), nil
}

// RotateURL closes the current RabbitMQ connection, reconnects with the
// new URL, and re-declares the queue. If reconnect fails, it falls back
// to the previous URL and attempts to reconnect with it.
func (q *Queue) RotateURL(newURL string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	oldURL := q.url
	q.url = newURL

	if q.channel != nil {
		q.channel.Close()
	}
	if q.conn != nil {
		q.conn.Close()
	}

	if err := q.connect(); err != nil {
		queueLog.Warn("rotate URL failed with new URL, falling back to previous", "error", err)
		q.url = oldURL
		if fallbackErr := q.connect(); fallbackErr != nil {
			queueLog.Error("fallback reconnect also failed", "error", fallbackErr)
			q.lastRotatedAt = now
			q.lastResult = "failed"
			q.lastError = err.Error()
			return fmt.Errorf("rotate failed: %w (fallback also failed: %v)", err, fallbackErr)
		}
		queueLog.Info("fallback reconnect to previous URL succeeded")
		q.lastRotatedAt = now
		q.lastResult = "failed"
		q.lastError = "new URL failed, fell back to previous: " + err.Error()
		return fmt.Errorf("rotate failed, fell back to previous URL: %w", err)
	}

	queueLog.Info("URL rotated successfully")
	q.lastRotatedAt = now
	q.lastResult = "success"
	q.lastError = ""
	return nil
}

// RotationStatus returns the timestamp, result, and error of the last
// rotation attempt. Safe to call concurrently.
func (q *Queue) RotationStatus() (time.Time, string, string) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.lastRotatedAt, q.lastResult, q.lastError
}

// Close shuts down the RabbitMQ connection.
func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	var err error
	if q.channel != nil {
		err = q.channel.Close()
	}
	if q.conn != nil {
		if cErr := q.conn.Close(); cErr != nil {
			err = cErr
		}
	}
	return err
}
