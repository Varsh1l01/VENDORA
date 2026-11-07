// Package kafka provides a durable producer, an at-least-once consumer with
// retries and dead-lettering, and topic provisioning.
package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	kgo "github.com/segmentio/kafka-go"

	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/pkg/logx"
	"github.com/Varsh1l01/VENDORA/pkg/requestid"
)

var (
	consumed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "events_consumed_total", Help: "Events processed by consumers.",
	}, []string{"group", "type", "result"})
	dlqTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "events_dlq_total", Help: "Events sent to a dead-letter topic.",
	}, []string{"group", "topic"})
)

func init() { prometheus.MustRegister(consumed, dlqTotal) }

// NewWriter returns a writer that waits for all in-sync replicas, routes by
// message key and takes the topic from each message.
func NewWriter(brokers []string) *kgo.Writer {
	return &kgo.Writer{
		Addr:                   kgo.TCP(brokers...),
		Balancer:               &kgo.Hash{},
		RequiredAcks:           kgo.RequireAll,
		BatchTimeout:           10 * time.Millisecond,
		WriteTimeout:           10 * time.Second,
		AllowAutoTopicCreation: false,
	}
}

// Message converts an envelope into a keyed Kafka message for topic.
func Message(topic string, ev events.Envelope) (kgo.Message, error) {
	b, err := json.Marshal(ev)
	if err != nil {
		return kgo.Message{}, err
	}
	return kgo.Message{
		Topic: topic,
		Key:   []byte(ev.AggregateID),
		Value: b,
		Headers: []kgo.Header{
			{Key: "event_type", Value: []byte(ev.Type)},
			{Key: requestid.MetadataKey, Value: []byte(ev.RequestID)},
		},
	}, nil
}

// EnsureTopics creates topics (idempotently) with the given partitions.
func EnsureTopics(ctx context.Context, brokers []string, partitions, replication int, topics ...string) error {
	var conn *kgo.Conn
	var err error
	backoff := 500 * time.Millisecond
	for {
		conn, err = kgo.DialContext(ctx, "tcp", brokers[0])
		if err == nil {
			break
		}
		slog.Warn("kafka not ready, retrying", "error", err)
		select {
		case <-ctx.Done():
			return fmt.Errorf("dial kafka: %w", err)
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 5*time.Second)
	}
	defer conn.Close()
	ctrl, err := conn.Controller()
	if err != nil {
		return fmt.Errorf("find controller: %w", err)
	}
	cc, err := kgo.DialContext(ctx, "tcp", net.JoinHostPort(ctrl.Host, strconv.Itoa(ctrl.Port)))
	if err != nil {
		return fmt.Errorf("dial controller: %w", err)
	}
	defer cc.Close()
	cfgs := make([]kgo.TopicConfig, 0, len(topics))
	for _, t := range topics {
		cfgs = append(cfgs, kgo.TopicConfig{Topic: t, NumPartitions: partitions, ReplicationFactor: replication})
	}
	if err := cc.CreateTopics(cfgs...); err != nil && !errors.Is(err, kgo.TopicAlreadyExists) {
		return fmt.Errorf("create topics: %w", err)
	}
	return nil
}

// Handler processes one event. Returning an error triggers a retry; after
// MaxAttempts the event is dead-lettered. Handlers must be idempotent.
type Handler func(ctx context.Context, ev events.Envelope) error

// ConsumerConfig configures a Consumer.
type ConsumerConfig struct {
	Brokers     []string
	GroupID     string
	Topics      []string
	MaxAttempts int
	BaseBackoff time.Duration
}

// Consumer reads events with at-least-once semantics: offsets are committed
// only after the handler succeeded or the event was dead-lettered.
type Consumer struct {
	cfg     ConsumerConfig
	reader  *kgo.Reader
	dlq     *kgo.Writer
	handler Handler
}

// NewConsumer builds a consumer for the configured group and topics.
func NewConsumer(cfg ConsumerConfig, h Handler) *Consumer {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 200 * time.Millisecond
	}
	return &Consumer{
		cfg: cfg,
		reader: kgo.NewReader(kgo.ReaderConfig{
			Brokers:        cfg.Brokers,
			GroupID:        cfg.GroupID,
			GroupTopics:    cfg.Topics,
			MinBytes:       1,
			MaxBytes:       10 << 20,
			MaxWait:        500 * time.Millisecond,
			StartOffset:    kgo.FirstOffset,
			CommitInterval: 0, // synchronous commits
		}),
		dlq:     NewWriter(cfg.Brokers),
		handler: h,
	}
}

// Run consumes until ctx is cancelled.
func (c *Consumer) Run(ctx context.Context) error {
	defer c.reader.Close()
	defer c.dlq.Close()
	backoff := c.cfg.BaseBackoff
	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("kafka fetch failed", "group", c.cfg.GroupID, "error", err)
			if !sleep(ctx, backoff) {
				return nil
			}
			backoff = min(backoff*2, 10*time.Second)
			continue
		}
		backoff = c.cfg.BaseBackoff
		c.process(ctx, msg)
		if ctx.Err() != nil {
			// Shutting down mid-processing: do not commit; the event will be
			// redelivered and handlers are idempotent.
			return nil
		}
		if err := c.reader.CommitMessages(ctx, msg); err != nil && ctx.Err() == nil {
			slog.Error("kafka commit failed", "group", c.cfg.GroupID, "error", err)
		}
	}
}

func (c *Consumer) process(ctx context.Context, msg kgo.Message) {
	var ev events.Envelope
	if err := json.Unmarshal(msg.Value, &ev); err != nil || ev.ID == "" || ev.Type == "" {
		c.deadLetter(ctx, msg, "", fmt.Errorf("malformed envelope: %w", err))
		return
	}
	hctx := requestid.With(ctx, ev.RequestID)
	hctx = logx.With(hctx, "event_id", ev.ID, "event_type", ev.Type, "request_id", ev.RequestID, "group", c.cfg.GroupID)
	var lastErr error
	for attempt := 1; attempt <= c.cfg.MaxAttempts; attempt++ {
		if lastErr = c.handler(hctx, ev); lastErr == nil {
			consumed.WithLabelValues(c.cfg.GroupID, ev.Type, "ok").Inc()
			return
		}
		if ctx.Err() != nil {
			return
		}
		logx.From(hctx).Warn("event handler failed", "attempt", attempt, "error", lastErr)
		// Exponential backoff with full jitter.
		d := c.cfg.BaseBackoff * time.Duration(1<<(attempt-1))
		if !sleep(ctx, time.Duration(rand.Int64N(int64(d))+1)) {
			return
		}
	}
	consumed.WithLabelValues(c.cfg.GroupID, ev.Type, "dlq").Inc()
	c.deadLetter(hctx, msg, ev.Type, lastErr)
}

func (c *Consumer) deadLetter(ctx context.Context, msg kgo.Message, evType string, cause error) {
	dlqTopic := events.DLQ(msg.Topic)
	out := kgo.Message{
		Topic: dlqTopic,
		Key:   msg.Key,
		Value: msg.Value,
		Headers: append(msg.Headers,
			kgo.Header{Key: "dlq_error", Value: []byte(fmt.Sprint(cause))},
			kgo.Header{Key: "dlq_group", Value: []byte(c.cfg.GroupID)},
			kgo.Header{Key: "dlq_source_partition", Value: []byte(strconv.Itoa(msg.Partition))},
			kgo.Header{Key: "dlq_source_offset", Value: []byte(strconv.FormatInt(msg.Offset, 10))},
		),
	}
	// Keep trying: losing a dead letter would lose the event.
	backoff := c.cfg.BaseBackoff
	for {
		err := c.dlq.WriteMessages(ctx, out)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return
		}
		slog.Error("dead-letter write failed, retrying", "topic", dlqTopic, "error", err)
		if !sleep(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, 10*time.Second)
	}
	dlqTotal.WithLabelValues(c.cfg.GroupID, msg.Topic).Inc()
	logx.From(ctx).Error("event dead-lettered", "topic", dlqTopic, "type", evType, "error", cause)
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
