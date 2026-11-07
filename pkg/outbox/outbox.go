// Package outbox implements the transactional outbox and the consumer-side
// inbox (processed events) for exactly-once effects over at-least-once
// delivery.
//
// Producers call Enqueue inside the same database transaction that changes
// state. A Relay publishes pending rows to Kafka. Many replicas can relay
// concurrently: rows are claimed with FOR UPDATE SKIP LOCKED, so no leader
// election is needed.
//
// Required tables: see services/*/migrations/000_outbox.sql.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	kgo "github.com/segmentio/kafka-go"

	"github.com/Varsh1l01/VENDORA/pkg/events"
	"github.com/Varsh1l01/VENDORA/pkg/kafka"
)

var (
	published = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "outbox_published_total", Help: "Outbox events published to Kafka.",
	}, []string{"service"})
	backlog = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "outbox_unpublished_rows", Help: "Outbox rows waiting to be published.",
	}, []string{"service"})
	oldest = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "outbox_oldest_unpublished_age_seconds", Help: "Age of the oldest unpublished outbox row.",
	}, []string{"service"})
)

func init() { prometheus.MustRegister(published, backlog, oldest) }

// Enqueue records ev for publication to topic inside tx.
func Enqueue(ctx context.Context, tx pgx.Tx, topic string, ev events.Envelope) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (id, topic, aggregate_id, event_type, envelope) VALUES ($1, $2, $3, $4, $5)`,
		ev.ID, topic, ev.AggregateID, ev.Type, b)
	if err != nil {
		return fmt.Errorf("insert outbox: %w", err)
	}
	return nil
}

// Writer is the subset of *kafka.Writer used by the relay.
type Writer interface {
	WriteMessages(ctx context.Context, msgs ...kgo.Message) error
}

// Relay publishes outbox rows.
type Relay struct {
	Pool      *pgxpool.Pool
	Writer    Writer
	Service   string
	BatchSize int
	Interval  time.Duration
	Retention time.Duration
}

// Run polls and publishes until ctx is cancelled.
func (r *Relay) Run(ctx context.Context) error {
	if r.BatchSize <= 0 {
		r.BatchSize = 100
	}
	if r.Interval <= 0 {
		r.Interval = 200 * time.Millisecond
	}
	if r.Retention <= 0 {
		r.Retention = 72 * time.Hour
	}
	stats := time.NewTicker(10 * time.Second)
	defer stats.Stop()
	backoff := r.Interval
	for {
		n, err := r.PublishBatch(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			slog.Warn("outbox publish failed", "error", err)
			backoff = min(backoff*2, 10*time.Second)
		case err == nil:
			backoff = r.Interval
		}
		// Drain quickly while there is a backlog, otherwise wait for the tick.
		if err == nil && n == r.BatchSize {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-stats.C:
			r.recordStats(ctx)
		case <-time.After(backoff):
		}
	}
}

// PublishBatch publishes up to BatchSize pending rows and returns how many.
func (r *Relay) PublishBatch(ctx context.Context) (int, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	rows, err := tx.Query(ctx, `
		SELECT id, topic, envelope FROM outbox
		WHERE published_at IS NULL
		ORDER BY created_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, r.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("select outbox: %w", err)
	}
	var ids []string
	var msgs []kgo.Message
	for rows.Next() {
		var id, topic string
		var raw []byte
		if err := rows.Scan(&id, &topic, &raw); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan outbox: %w", err)
		}
		var ev events.Envelope
		if err := json.Unmarshal(raw, &ev); err != nil {
			rows.Close()
			return 0, fmt.Errorf("decode outbox %s: %w", id, err)
		}
		m, err := kafka.Message(topic, ev)
		if err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
		msgs = append(msgs, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(msgs) == 0 {
		return 0, nil
	}
	if err := r.Writer.WriteMessages(ctx, msgs...); err != nil {
		return 0, fmt.Errorf("write kafka: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, ids); err != nil {
		return 0, fmt.Errorf("mark published: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		// Messages were written but not marked: they will be re-sent.
		// Consumers deduplicate by event id, so this is safe.
		return 0, fmt.Errorf("commit outbox: %w", err)
	}
	published.WithLabelValues(r.Service).Add(float64(len(msgs)))
	return len(msgs), nil
}

func (r *Relay) recordStats(ctx context.Context) {
	var n int64
	var age float64
	err := r.Pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)
		FROM outbox WHERE published_at IS NULL`).Scan(&n, &age)
	if err == nil {
		backlog.WithLabelValues(r.Service).Set(float64(n))
		oldest.WithLabelValues(r.Service).Set(age)
	}
	_, _ = r.Pool.Exec(ctx, `DELETE FROM outbox WHERE published_at < now() - $1::interval`,
		fmt.Sprintf("%d seconds", int(r.Retention.Seconds())))
}

// MarkProcessed records that consumer handled eventID inside tx. It returns
// false when the event was already processed (duplicate delivery), in which
// case the caller must skip its side effects.
func MarkProcessed(ctx context.Context, tx pgx.Tx, consumer, eventID string) (bool, error) {
	tag, err := tx.Exec(ctx,
		`INSERT INTO processed_events (consumer, event_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		consumer, eventID)
	if err != nil {
		return false, fmt.Errorf("mark processed: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
