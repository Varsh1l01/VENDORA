// Package db opens tuned PostgreSQL pools and applies embedded migrations.
package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Varsh1l01/VENDORA/pkg/config"
)

// PoolConfig tunes the connection pool.
type PoolConfig struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// LoadPoolConfig reads pool settings using prefix (e.g. "ORDER_DB").
func LoadPoolConfig(l *config.Loader, prefix string) PoolConfig {
	return PoolConfig{
		URL:             l.Required(prefix + "_URL"),
		MaxConns:        int32(l.Int(prefix+"_MAX_CONNS", 20)),
		MinConns:        int32(l.Int(prefix+"_MIN_CONNS", 2)),
		MaxConnLifetime: l.Duration(prefix+"_MAX_CONN_LIFETIME", 30*time.Minute),
		MaxConnIdleTime: l.Duration(prefix+"_MAX_CONN_IDLE", 5*time.Minute),
	}
}

// Open creates a pool, waits until the database answers (bounded by ctx) and
// registers pool metrics under name.
func Open(ctx context.Context, c PoolConfig, name string) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(c.URL)
	if err != nil {
		return nil, fmt.Errorf("parse db url: %w", err)
	}
	if c.MaxConns > 0 {
		pc.MaxConns = c.MaxConns
	}
	pc.MinConns = c.MinConns
	pc.MaxConnLifetime = c.MaxConnLifetime
	pc.MaxConnIdleTime = c.MaxConnIdleTime
	pc.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	backoff := 250 * time.Millisecond
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err == nil {
			break
		}
		slog.Warn("database not ready, retrying", "db", name, "error", err)
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, fmt.Errorf("ping %s: %w", name, err)
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 5*time.Second)
	}
	registerPoolMetrics(pool, name)
	return pool, nil
}

// IsUniqueViolation reports whether err is a unique constraint violation.
func IsUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// IsCheckViolation reports whether err is a CHECK constraint violation.
func IsCheckViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23514"
}

// IsNoRows reports whether err is pgx.ErrNoRows.
func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// InTx runs fn in a transaction, committing on success and rolling back on
// error or panic.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) (err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// migrationLockID is an arbitrary constant for pg_advisory_lock so that
// concurrently starting replicas apply migrations exactly once.
const migrationLockID = 7_412_221_901

// Migrate applies *.sql files from fsys (sorted by name) that have not been
// applied yet. Each file runs in its own transaction and is recorded in
// schema_migrations. Safe to run from multiple replicas concurrently.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockID) }()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, f := range files {
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, f).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", f, err)
		}
		if exists {
			continue
		}
		body, err := fs.ReadFile(fsys, f)
		if err != nil {
			return fmt.Errorf("read %s: %w", f, err)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", f, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES ($1)`, f); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record %s: %w", f, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit %s: %w", f, err)
		}
		slog.Info("migration applied", "version", f)
	}
	return nil
}

type poolCollector struct {
	pool *pgxpool.Pool
	acq  *prometheus.Desc
	idle *prometheus.Desc
	tot  *prometheus.Desc
	max  *prometheus.Desc
	wait *prometheus.Desc
	wdur *prometheus.Desc
}

func registerPoolMetrics(pool *pgxpool.Pool, name string) {
	l := prometheus.Labels{"db": name}
	c := &poolCollector{
		pool: pool,
		acq:  prometheus.NewDesc("db_pool_acquired_conns", "Connections in use.", nil, l),
		idle: prometheus.NewDesc("db_pool_idle_conns", "Idle connections.", nil, l),
		tot:  prometheus.NewDesc("db_pool_total_conns", "Total connections.", nil, l),
		max:  prometheus.NewDesc("db_pool_max_conns", "Max connections.", nil, l),
		wait: prometheus.NewDesc("db_pool_empty_acquire_total", "Acquires that had to wait.", nil, l),
		wdur: prometheus.NewDesc("db_pool_acquire_wait_seconds_total", "Total time waiting for a connection.", nil, l),
	}
	// Ignore duplicate registration (e.g. tests opening several pools).
	_ = prometheus.Register(c)
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.acq
	ch <- c.idle
	ch <- c.tot
	ch <- c.max
	ch <- c.wait
	ch <- c.wdur
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()
	ch <- prometheus.MustNewConstMetric(c.acq, prometheus.GaugeValue, float64(s.AcquiredConns()))
	ch <- prometheus.MustNewConstMetric(c.idle, prometheus.GaugeValue, float64(s.IdleConns()))
	ch <- prometheus.MustNewConstMetric(c.tot, prometheus.GaugeValue, float64(s.TotalConns()))
	ch <- prometheus.MustNewConstMetric(c.max, prometheus.GaugeValue, float64(s.MaxConns()))
	ch <- prometheus.MustNewConstMetric(c.wait, prometheus.CounterValue, float64(s.EmptyAcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.wdur, prometheus.CounterValue, s.EmptyAcquireWaitTime().Seconds())
}
