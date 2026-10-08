// Package postgres contains SQL repositories and explicit transaction boundaries.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Store struct{ db DBTX }

func New(db DBTX) *Store { return &Store{db: db} }

func Open(ctx context.Context, dsn string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid PostgreSQL connection configuration")
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 0
	cfg.ConnConfig.ConnectTimeout = 10 * time.Second
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.MaxConnLifetime = time.Hour
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return pool, nil
}

func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(*Store) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if err := fn(New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// WorkerLock holds a dedicated session until Close; it never returns a locked
// connection to the pool. Losing this connection must stop the worker.
type WorkerLock struct {
	mu   sync.Mutex
	conn *pgxpool.Conn
}

const workerLockID int64 = 0x646f67676f

func LockWorker(ctx context.Context, pool *pgxpool.Pool) (*WorkerLock, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", workerLockID).Scan(&locked); err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = conn.Hijack().Close(cleanup)
		cancel()
		return nil, err
	}
	if !locked {
		conn.Release()
		return nil, errors.New("another worker is already active")
	}
	return &WorkerLock{conn: conn}, nil
}
func (l *WorkerLock) Ping(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn == nil {
		return errors.New("worker lock is closed")
	}
	return l.conn.Conn().Ping(ctx)
}
func (l *WorkerLock) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Wait for server acknowledgement before allowing an immediate restart.
	// Terminating the TCP session alone can race with the next lock attempt.
	var unlocked bool
	unlockErr := l.conn.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", workerLockID).Scan(&unlocked)
	if unlockErr == nil && !unlocked {
		unlockErr = errors.New("worker lock was not held")
	}
	// Always close the physical session, even if explicit unlocking failed.
	conn := l.conn.Hijack()
	l.conn = nil
	return errors.Join(unlockErr, conn.Close(ctx))
}
