package edgecert

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/edgecert/certpg/sqlc"
)

// NotifyChannel is the pg_notify channel a new certificate is announced on;
// the payload is its row id.
const NotifyChannel = "edge_tls_certificate"

// Store keeps the certificates. edgednsd is its only writer.
type Store interface {
	// NewestTerms returns the newest certificate for names from ca.
	NewestTerms(ctx context.Context, names []string, ca string) (Terms, bool, error)
	// Insert stores c and announces it on NotifyChannel when it commits.
	Insert(ctx context.Context, c Certificate) (int64, error)
	// DeleteExpiredBefore drops the certificates that expired before t.
	DeleteExpiredBefore(ctx context.Context, t time.Time) (int64, error)
}

// PgStore is the Store over Postgres.
type PgStore struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

// NewPgStore returns the Store over pool.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool, q: sqlc.New()}
}

func (s *PgStore) NewestTerms(ctx context.Context, names []string, ca string) (Terms, bool, error) {
	row, err := s.q.NewestEdgeTLSCertificateTerms(ctx, s.pool, sqlc.NewestEdgeTLSCertificateTermsParams{Names: names, Ca: ca})
	if errors.Is(err, pgx.ErrNoRows) {
		return Terms{}, false, nil
	}
	if err != nil {
		return Terms{}, false, fmt.Errorf("edgecert: read the newest certificate: %w", err)
	}
	return Terms{AccountURI: row.AccountUri, KeyKID: row.KeyKid, SHA256: row.Sha256, NotAfter: row.NotAfter.Time}, true, nil
}

func (s *PgStore) Insert(ctx context.Context, c Certificate) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("edgecert: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, err := s.q.InsertEdgeTLSCertificate(ctx, tx, sqlc.InsertEdgeTLSCertificateParams{
		Names:      c.Names,
		Ca:         c.CA,
		AccountUri: c.AccountURI,
		Serial:     c.Serial,
		Sha256:     c.SHA256,
		ChainPem:   c.ChainPEM,
		KeySealed:  c.KeySealed,
		KeyKid:     c.KeyKID,
		NotBefore:  pgtype.Timestamptz{Time: c.NotBefore, Valid: true},
		NotAfter:   pgtype.Timestamptz{Time: c.NotAfter, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("edgecert: store the certificate: %w", err)
	}
	if err := s.q.NotifyEdgeTLSCertificate(ctx, tx, strconv.FormatInt(id, 10)); err != nil {
		return 0, fmt.Errorf("edgecert: announce the certificate: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("edgecert: commit the certificate: %w", err)
	}
	return id, nil
}

func (s *PgStore) DeleteExpiredBefore(ctx context.Context, t time.Time) (int64, error) {
	n, err := s.q.DeleteEdgeTLSCertificatesExpiredBefore(ctx, s.pool, pgtype.Timestamptz{Time: t, Valid: true})
	if err != nil {
		return 0, fmt.Errorf("edgecert: drop expired certificates: %w", err)
	}
	return n, nil
}
