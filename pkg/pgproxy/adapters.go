package pgproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/overlay"
	"github.com/onebox-faas/faas/pkg/pgscram"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// VerifierStore reads and writes database_role_verifiers. apid's native
// driver writes; pgproxyd reads through a short cache.
type VerifierStore struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
	ttl  time.Duration
	now  func() time.Time

	mu    sync.Mutex
	cache map[string]cachedVerifier
}

type cachedVerifier struct {
	v       pgscram.Verifier
	expires time.Time
}

// NewVerifierStore caches positive lookups for ttl; misses are never
// cached, so a new role works on its first connection.
func NewVerifierStore(pool *pgxpool.Pool, ttl time.Duration) (*VerifierStore, error) {
	if pool == nil {
		return nil, errors.New("pgproxy: nil pool")
	}
	return &VerifierStore{pool: pool, q: sqlc.New(), ttl: ttl, now: time.Now, cache: map[string]cachedVerifier{}}, nil
}

var _ Credentials = (*VerifierStore)(nil)

func databaseUUID(id string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}, ErrNoVerifier
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func (s *VerifierStore) Verifier(ctx context.Context, databaseID, role string) (pgscram.Verifier, error) {
	key := databaseID + "\x00" + role
	s.mu.Lock()
	if hit, ok := s.cache[key]; ok && s.now().Before(hit.expires) {
		s.mu.Unlock()
		return hit.v, nil
	}
	s.mu.Unlock()
	id, err := databaseUUID(databaseID)
	if err != nil {
		return pgscram.Verifier{}, err
	}
	raw, err := s.q.GetDatabaseRoleVerifier(ctx, s.pool, sqlc.GetDatabaseRoleVerifierParams{DatabaseID: id, RoleName: role})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgscram.Verifier{}, ErrNoVerifier
	}
	if err != nil {
		return pgscram.Verifier{}, fmt.Errorf("pgproxy: verifier lookup: %w", err)
	}
	v, err := pgscram.ParseVerifier(raw)
	if err != nil {
		return pgscram.Verifier{}, fmt.Errorf("pgproxy: stored verifier: %w", err)
	}
	if s.ttl > 0 {
		s.mu.Lock()
		s.cache[key] = cachedVerifier{v: v, expires: s.now().Add(s.ttl)}
		s.mu.Unlock()
	}
	return v, nil
}

// Put stores a role's verifier.
func (s *VerifierStore) Put(ctx context.Context, databaseID, role string, v pgscram.Verifier) error {
	id, err := databaseUUID(databaseID)
	if err != nil {
		return err
	}
	return s.q.UpsertDatabaseRoleVerifier(ctx, s.pool, sqlc.UpsertDatabaseRoleVerifierParams{DatabaseID: id, RoleName: role, Verifier: v.String()})
}

// Delete removes a role's verifier; the proxy refuses the role at once on
// cache miss and within the cache TTL otherwise.
func (s *VerifierStore) Delete(ctx context.Context, databaseID, role string) error {
	id, err := databaseUUID(databaseID)
	if err != nil {
		return err
	}
	_, err = s.q.DeleteDatabaseRoleVerifier(ctx, s.pool, sqlc.DeleteDatabaseRoleVerifierParams{DatabaseID: id, RoleName: role})
	return err
}

// ScheddWaker wakes databases through schedd.
type ScheddWaker struct {
	client scheddpb.ScheddClient
}

// NewScheddWaker wraps a schedd client authenticated as pgproxyd.
func NewScheddWaker(client scheddpb.ScheddClient) *ScheddWaker {
	return &ScheddWaker{client: client}
}

func (w *ScheddWaker) Wake(ctx context.Context, databaseID string) (string, error) {
	rt, err := w.client.WakeDatabase(ctx, &scheddpb.DatabaseRef{DatabaseId: databaseID})
	if err != nil {
		return "", err
	}
	if rt.GetVmmdTarget() == "" {
		return "", errors.New("pgproxy: schedd returned no vmmd target")
	}
	return rt.GetVmmdTarget(), nil
}

func (w *ScheddWaker) Touch(ctx context.Context, databaseID string) error {
	_, err := w.client.TouchDatabase(ctx, &scheddpb.DatabaseRef{DatabaseId: databaseID})
	return err
}

// VmmdDialer opens sessions through vmmd's DatabaseSession stream. It keeps
// one connection per vmmd target.
type VmmdDialer struct {
	tls  *tls.Config
	dial func(ctx context.Context, target string, tlsCfg *tls.Config) (*grpc.ClientConn, error)

	mu    sync.Mutex
	conns map[string]*grpc.ClientConn
}

// NewVmmdDialer dials vmmd through the overlay with pgproxyd's client mTLS.
func NewVmmdDialer(tlsCfg *tls.Config) *VmmdDialer {
	return NewVmmdDialerWith(tlsCfg, func(ctx context.Context, target string, tlsCfg *tls.Config) (*grpc.ClientConn, error) {
		return overlay.Dial(ctx, overlay.New(target), tlsCfg)
	})
}

// NewVmmdDialerWith is NewVmmdDialer with an injected dialer.
func NewVmmdDialerWith(tlsCfg *tls.Config, dial func(context.Context, string, *tls.Config) (*grpc.ClientConn, error)) *VmmdDialer {
	return &VmmdDialer{tls: tlsCfg, dial: dial, conns: map[string]*grpc.ClientConn{}}
}

var _ SessionDialer = (*VmmdDialer)(nil)

func (d *VmmdDialer) client(ctx context.Context, target string) (vmmdpb.VmmdClient, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	conn, ok := d.conns[target]
	if !ok {
		var err error
		if conn, err = d.dial(ctx, target, d.tls); err != nil {
			return nil, err
		}
		d.conns[target] = conn
	}
	return vmmdpb.NewVmmdClient(conn), nil
}

// Dial opens a session. The stream outlives ctx, which only bounds setup;
// Close ends it.
func (d *VmmdDialer) Dial(ctx context.Context, target, databaseID string) (io.ReadWriteCloser, error) {
	c, err := d.client(ctx, target)
	if err != nil {
		return nil, err
	}
	// The session must outlive the setup context, so it keeps ctx's values
	// but not its cancellation.
	streamCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stream, err := c.DatabaseSession(streamCtx)
	if err != nil {
		cancel()
		return nil, err
	}
	if err := stream.Send(&vmmdpb.DatabaseSessionFrame{DatabaseId: databaseID}); err != nil {
		cancel()
		return nil, err
	}
	return &sessionStream{stream: stream, cancel: cancel}, nil
}

// Close releases every vmmd connection.
func (d *VmmdDialer) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for target, conn := range d.conns {
		_ = conn.Close()
		delete(d.conns, target)
	}
	return nil
}

// sessionStream adapts a DatabaseSession stream to io.ReadWriteCloser.
type sessionStream struct {
	stream  vmmdpb.Vmmd_DatabaseSessionClient
	cancel  context.CancelFunc
	pending []byte
	writeMu sync.Mutex
}

const maxSessionFrame = 64 << 10

func (s *sessionStream) Read(p []byte) (int, error) {
	for len(s.pending) == 0 {
		frame, err := s.stream.Recv()
		if err != nil {
			return 0, io.EOF
		}
		s.pending = frame.GetData()
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

func (s *sessionStream) Write(p []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	written := 0
	for written < len(p) {
		end := min(written+maxSessionFrame, len(p))
		chunk := append([]byte(nil), p[written:end]...)
		if err := s.stream.Send(&vmmdpb.DatabaseSessionFrame{Data: chunk}); err != nil {
			return written, err
		}
		written = end
	}
	return written, nil
}

func (s *sessionStream) Close() error {
	_ = s.stream.CloseSend()
	s.cancel()
	return nil
}
