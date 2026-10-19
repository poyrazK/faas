package postgresprobe

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	probesql "github.com/onebox-faas/faas/pkg/managedpostgres/credentialdelivery/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialprobe"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

const poolConcurrency = int64(16)

// RestorePath is the disposable workload's guest-local reconnect callback.
const RestorePath = "/internal/after-restore"

// PoolWorkload is the disposable customer's long-lived SQL client. Rotation
// replaces the guest and its configuration; a restore reconnects the existing
// configuration. It never reloads credentials or retries a SQL mutation.
type PoolWorkload struct {
	config         Config
	writer, reader *pgxpool.Pool
	gate           *semaphore.Weighted
	generation     uint64 // Protected by gate: checks borrow one, restore borrows all.
	ready          atomic.Bool
	closed         atomic.Bool
	closeOnce      sync.Once
}

func NewPoolWorkload(ctx context.Context, config Config) (*PoolWorkload, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := config.key(); err != nil || config.RuntimeURI == "" || config.ReaderURI == "" || config.MigrationURI != "" {
		return nil, ErrProbe
	}
	if credentialprobe.VerifyCredentialSQL(ctx, config.RuntimeURI, credentialprobe.ReadWrite, config.PostgresMajor) != nil || credentialprobe.VerifyCredentialSQL(ctx, config.ReaderURI, credentialprobe.ReadOnly, config.PostgresMajor) != nil {
		return nil, ErrProbe
	}
	p := &PoolWorkload{config: config, gate: semaphore.NewWeighted(poolConcurrency), generation: 1}
	var err error
	p.writer, err = openPool(ctx, config.RuntimeURI)
	if err != nil {
		return nil, ErrProbe
	}
	p.reader, err = openPool(ctx, config.ReaderURI)
	if err != nil {
		p.writer.Close()
		return nil, ErrProbe
	}
	if err := p.readMarker(ctx); err != nil {
		// Construction has not published the workload or started checks.
		p.reader.Close()
		p.writer.Close()
		return nil, ErrProbe
	}
	p.ready.Store(true)
	return p, nil
}

func openPool(ctx context.Context, uri string) (*pgxpool.Pool, error) {
	conn, err := credentialprobe.CredentialSQLConfig(uri)
	if err != nil {
		return nil, ErrProbe
	}
	config, err := pgxpool.ParseConfig(uri)
	if err != nil {
		return nil, ErrProbe
	}
	config.ConnConfig = conn
	// pgxpool can continue dialing after a canceled acquire. Bound that work
	// independently, rather than inheriting its two-minute default.
	config.ConnConfig.ConnectTimeout = 2 * time.Second
	config.MaxConns = 4
	config.MinConns = 0
	config.PingTimeout = time.Second
	return pgxpool.NewWithConfig(ctx, config)
}

func (p *PoolWorkload) readMarker(ctx context.Context) error {
	id, _ := p.config.key()
	queries := probesql.New()
	group, readCtx := errgroup.WithContext(ctx)
	// Independent read-only checks share one hook deadline. Reconnecting
	// serially needlessly adds the writer and reader connection latencies (ADR-348).
	for _, pool := range []*pgxpool.Pool{p.writer, p.reader} {
		group.Go(func() error {
			row, err := queries.ReadQualificationMarker(readCtx, pool, id)
			if err != nil || row.Marker != p.config.marker() {
				return ErrProbe
			}
			return nil
		})
	}
	return group.Wait()
}

// Restore is a readiness barrier: discard the snapshot's sockets, authenticate
// new sessions and verify existing data without writing or repairing it. An
// unavailable SQL service rejects this restored guest so Gregale can cold boot.
func (p *PoolWorkload) Restore(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	if err := p.gate.Acquire(ctx, poolConcurrency); err != nil {
		return ErrProbe
	}
	defer p.gate.Release(poolConcurrency)
	if p.closed.Load() {
		return ErrProbe
	}
	p.ready.Store(false)
	p.writer.Reset()
	p.reader.Reset()
	if err := p.readMarker(ctx); err != nil {
		return ErrProbe
	}
	p.generation++
	p.ready.Store(true)
	return nil
}

func (p *PoolWorkload) Check(ctx context.Context) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := p.gate.Acquire(ctx, 1); err != nil {
		return Result{}, ErrProbe
	}
	defer p.gate.Release(1)
	if p.closed.Load() || !p.ready.Load() {
		return Result{}, ErrProbe
	}
	id, _ := p.config.key()
	queries := probesql.New()
	before, err := queries.ReadQualificationMarker(ctx, p.reader, id)
	if err != nil || before.Marker != p.config.marker() {
		return Result{}, ErrProbe
	}
	// A failed or uncertain write is returned to the caller, never replayed.
	// Keep cancellation before COMMIT from finishing an autocommit statement
	// after the caller has left. COMMIT errors can still have uncertain outcomes.
	tx, err := p.writer.Begin(ctx)
	if err != nil {
		return Result{}, ErrProbe
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	after, err := queries.AdvanceQualificationMarker(ctx, tx, id)
	if err != nil || after <= before.Counter {
		return Result{}, ErrProbe
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, ErrProbe
	}
	readback, err := queries.ReadQualificationMarker(ctx, p.reader, id)
	if err != nil || readback.Marker != p.config.marker() || readback.Counter < after {
		return Result{}, ErrProbe
	}
	return Result{Marker: p.config.marker(), Counter: after, RuntimeProof: Proof(p.config.RunID, p.config.RuntimeURI), ReaderProof: Proof(p.config.RunID, p.config.ReaderURI), PoolGeneration: p.generation, WriterConnections: p.writer.Stat().NewConnsCount(), ReaderConnections: p.reader.Stat().NewConnsCount()}, nil
}

func (p *PoolWorkload) Close() {
	p.closeOnce.Do(func() {
		p.closed.Store(true)
		p.ready.Store(false)
		_ = p.gate.Acquire(context.Background(), poolConcurrency)
		defer p.gate.Release(poolConcurrency)
		p.writer.Close()
		p.reader.Close()
	})
}

func (p *PoolWorkload) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == RestorePath {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			ip := net.ParseIP(host)
			if err != nil || ip == nil || !ip.IsLoopback() || r.Header.Get("X-Faas-After-Restore") != "1" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if p.Restore(r.Context()) != nil {
				http.Error(w, ErrProbe.Error(), http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !p.ready.Load() || p.closed.Load() {
			http.Error(w, ErrProbe.Error(), http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/probe" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		result, err := p.Check(r.Context())
		if err != nil {
			http.Error(w, ErrProbe.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}
