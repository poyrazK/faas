package mcpmanagement

import (
	"context"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/mcpmanagement/statepg/sqlc"
)

// The hosted bridge keeps its state in Postgres (ADR-20261026195952). Each
// namespace is one row sealed under the bridge's key, with the namespace in
// the additional data, and every write commits only while this process still
// holds the bridge's lease at the epoch it acquired.
const (
	defaultStateLeaseTTL = 30 * time.Second
	stateCommitTimeout   = 15 * time.Second
)

var bridgeName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ErrStateLeaseLost reports that another process holds the bridge's lease.
var ErrStateLeaseLost = errors.New("MCP state lease lost to another process")

// PostgresStateOptions configure the hosted bridge's state.
type PostgresStateOptions struct {
	Pool *pgxpool.Pool
	// Bridge names the lease and its rows, for example "hosted".
	Bridge string
	// KeyPath is the independent private 32-byte state key; Postgres holds
	// only ciphertext.
	KeyPath string
	// LeaseTTL defaults to 30 seconds; the lease is renewed every third of it.
	LeaseTTL time.Duration
	// AcquireTimeout bounds the wait for a predecessor's lease to expire. It
	// defaults to twice the TTL plus five seconds.
	AcquireTimeout time.Duration
	// OnLeaseLost runs once after the store has disabled access because the
	// lease was lost or could not be renewed in time. The process should exit.
	OnLeaseLost func(error)
}

// OpenPostgresStateStore takes the bridge's lease, waiting for an expired
// predecessor, and loads its state. Close stops renewal and releases the lease.
func OpenPostgresStateStore(ctx context.Context, opts PostgresStateOptions) (*StateStore, error) {
	if opts.Pool == nil || !bridgeName.MatchString(opts.Bridge) || opts.KeyPath == "" {
		return nil, errors.New("postgres MCP state needs a pool, a bridge name matching ^[a-z0-9][a-z0-9-]{0,62}$ and a key file")
	}
	if opts.LeaseTTL == 0 {
		opts.LeaseTTL = defaultStateLeaseTTL
	}
	if opts.LeaseTTL < 3*time.Second || opts.LeaseTTL > 10*time.Minute {
		return nil, errors.New("MCP state lease TTL must be between 3s and 10m")
	}
	if opts.AcquireTimeout == 0 {
		opts.AcquireTimeout = 2*opts.LeaseTTL + 5*time.Second
	}
	aead, err := readStateKey(opts.KeyPath)
	if err != nil {
		return nil, err
	}
	holder := make([]byte, 16)
	if _, err := rand.Read(holder); err != nil {
		return nil, err
	}
	p := &pgState{pool: opts.Pool, q: sqlc.New(), bridge: opts.Bridge, holder: hex.EncodeToString(holder), aead: aead, ttl: opts.LeaseTTL, onLost: opts.OnLeaseLost, stop: make(chan struct{}), done: make(chan struct{})}
	if err := p.acquire(ctx, opts.AcquireTimeout); err != nil {
		return nil, err
	}
	store, err := openStateStore(p)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.renewing = true
	p.mu.Unlock()
	go p.renew(store)
	return store, nil
}

type pgState struct {
	pool   *pgxpool.Pool
	q      *sqlc.Queries
	bridge string
	holder string
	aead   cipher.AEAD
	ttl    time.Duration
	onLost func(error)

	mu    sync.Mutex
	epoch int64
	// valid is when the lease surely expires if no renewal lands, by this
	// process's clock, which starts before the database's.
	valid    time.Time
	renewing bool

	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func (p *pgState) acquire(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		started := time.Now()
		epoch, err := p.q.AcquireMCPBridgeLease(ctx, p.pool, sqlc.AcquireMCPBridgeLeaseParams{Bridge: p.bridge, Holder: p.holder, TtlSeconds: p.ttl.Seconds()})
		if err == nil {
			p.epoch, p.valid = epoch, started.Add(p.ttl)
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("acquire MCP state lease: %w", err)
		}
		if time.Now().After(deadline) {
			return errors.New("MCP state is held by another running bridge")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// aad binds a sealed row to its bridge and namespace.
func (p *pgState) aad(namespace string) []byte {
	return []byte(stateFormat + "\x00postgres\x00" + p.bridge + "\x00" + namespace)
}

func (p *pgState) load() (map[string]json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), stateCommitTimeout)
	defer cancel()
	rows, err := p.q.ListMCPBridgeState(ctx, p.pool, p.bridge)
	if err != nil {
		return nil, fmt.Errorf("read MCP state: %w", err)
	}
	records := make(map[string]json.RawMessage, len(rows))
	n := p.aead.NonceSize()
	for _, row := range rows {
		if len(row.Sealed) < n {
			return nil, errors.New("invalid encrypted MCP state")
		}
		plain, err := p.aead.Open(nil, row.Sealed[:n], row.Sealed[n:], p.aad(row.Namespace))
		if err != nil {
			return nil, errors.New("MCP state authentication failed; use the original key and unmodified state")
		}
		if !json.Valid(plain) {
			return nil, errors.New("invalid MCP state records")
		}
		records[row.Namespace] = plain
	}
	return records, nil
}

func (p *pgState) commit(records map[string]json.RawMessage, changed []string) error {
	p.mu.Lock()
	epoch := p.epoch
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), stateCommitTimeout)
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := p.q.HoldMCPBridgeLease(ctx, tx, sqlc.HoldMCPBridgeLeaseParams{Bridge: p.bridge, Holder: p.holder, Epoch: epoch}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStateLeaseLost
		}
		return err
	}
	for _, namespace := range changed {
		data, ok := records[namespace]
		if !ok {
			if err := p.q.DeleteMCPBridgeState(ctx, tx, sqlc.DeleteMCPBridgeStateParams{Bridge: p.bridge, Namespace: namespace}); err != nil {
				return err
			}
			continue
		}
		nonce := make([]byte, p.aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		sealed := p.aead.Seal(nonce, nonce, data, p.aad(namespace))
		if len(namespace) > 256 || len(sealed) > maxStateBytes {
			return errors.New("MCP state capacity reached")
		}
		if err := p.q.PutMCPBridgeState(ctx, tx, sqlc.PutMCPBridgeStateParams{Bridge: p.bridge, Namespace: namespace, Sealed: sealed, Epoch: epoch}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// renew extends the lease every third of its TTL. A lease another process
// took, or one not renewed before it surely expired, disables the store.
func (p *pgState) renew(store *StateStore) {
	defer close(p.done)
	ticker := time.NewTicker(p.ttl / 3)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
		}
		p.mu.Lock()
		epoch := p.epoch
		p.mu.Unlock()
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), p.ttl/3)
		_, err := p.q.RenewMCPBridgeLease(ctx, p.pool, sqlc.RenewMCPBridgeLeaseParams{Bridge: p.bridge, Holder: p.holder, Epoch: epoch, TtlSeconds: p.ttl.Seconds()})
		cancel()
		if err == nil {
			p.mu.Lock()
			p.valid = started.Add(p.ttl)
			p.mu.Unlock()
			continue
		}
		p.mu.Lock()
		expired := time.Now().After(p.valid)
		p.mu.Unlock()
		if errors.Is(err, pgx.ErrNoRows) || expired {
			if !errors.Is(err, pgx.ErrNoRows) {
				err = fmt.Errorf("%w: renewal failed until it expired: %v", ErrStateLeaseLost, err)
			} else {
				err = ErrStateLeaseLost
			}
			store.fail(err)
			if p.onLost != nil {
				p.onLost(err)
			}
			return
		}
	}
}

func (p *pgState) close() error {
	var err error
	p.closeOnce.Do(func() {
		close(p.stop)
		p.mu.Lock()
		renewing, epoch := p.renewing, p.epoch
		p.mu.Unlock()
		if renewing {
			<-p.done
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = p.q.ReleaseMCPBridgeLease(ctx, p.pool, sqlc.ReleaseMCPBridgeLeaseParams{Bridge: p.bridge, Holder: p.holder, Epoch: epoch})
	})
	return err
}
