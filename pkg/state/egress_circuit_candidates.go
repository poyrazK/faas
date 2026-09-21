package state

// Egress circuit-breaker candidate reads (ADR-201 §3).

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// EgressCircuitCandidate is one opted-in upstream plus its newest probe
// verdict.
//
// Host is the plaintext value schedd resolves locally to write an nftables
// element. It MUST NOT reach a metric label, a log line, or the wire — those
// carry HostRedactedHash only (ADR-098 §11).
//
// Sampled is the zero time when the upstream has no probe inside the window.
// That is deliberately distinguishable from "row gone": the loop skips
// unprobed upstreams but must still treat them as live candidates so their
// dedupe state is not retired out from under them.
type EgressCircuitCandidate struct {
	AppID            string
	HostRedactedHash string
	Host             string
	Port             int
	// FailureThreshold / MinSamples / OpenSeconds are the per-upstream
	// overrides; nil means "track the platform default".
	FailureThreshold *float64
	MinSamples       *int
	OpenSeconds      *int
	OK               bool
	Sampled          time.Time
}

// ListEgressCircuitCandidates (ADR-201 §3) — MemStore stub, Postgres-only.
//
// Mirrors every other ADR-098 data_upstreams method on MemStore: the feed
// reads a partitioned probe table with a DISTINCT ON join, and a hand-rolled
// in-memory imitation of that is exactly the kind of divergence that produced
// the always-zero uppercase-state-literal queries. A unit test that reaches
// this should run against pgtest instead.
func (m *MemStore) ListEgressCircuitCandidates(_ context.Context, _ time.Time) ([]EgressCircuitCandidate, error) {
	return nil, errMemStoreDataUpstreams
}

// ListEgressCircuitCandidates returns every opted-in upstream joined to its
// newest probe verdict no older than `since`.
func (s *PgStore) ListEgressCircuitCandidates(ctx context.Context, since time.Time) ([]EgressCircuitCandidate, error) {
	rows, err := s.dataUpstreamsQueries().ListEgressCircuitCandidates(ctx, s.pool, pgtype.Timestamptz{Time: since, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("state: list egress circuit candidates: %w", err)
	}
	out := make([]EgressCircuitCandidate, 0, len(rows))
	for _, r := range rows {
		c := EgressCircuitCandidate{
			AppID:            uuidString(r.AppID),
			HostRedactedHash: r.HostRedactedHash,
			Host:             r.Host,
			Port:             int(r.Port),
		}
		if r.CircuitBreakerFailureThreshold.Valid {
			v := r.CircuitBreakerFailureThreshold.Float64
			c.FailureThreshold = &v
		}
		if r.CircuitBreakerMinSamples.Valid {
			v := int(r.CircuitBreakerMinSamples.Int32)
			c.MinSamples = &v
		}
		if r.CircuitBreakerOpenSeconds.Valid {
			v := int(r.CircuitBreakerOpenSeconds.Int32)
			c.OpenSeconds = &v
		}
		// A LEFT JOIN miss leaves both NULL. Guard on Sampled rather than on
		// Ok: a NULL `ok` would otherwise read as false, which is a FAILED
		// probe — the difference between "never measured" and "measured and
		// broken" is the difference between leaving a dependency alone and
		// cutting an app off from it.
		if r.SampledAt.Valid {
			c.Sampled = r.SampledAt.Time
			c.OK = r.Ok.Valid && r.Ok.Bool
		}
		out = append(out, c)
	}
	return out, nil
}

// UpdateDataUpstreamCircuitBreakerParams is the partial-update input for the
// ADR-201 §3 per-upstream policy. A nil field means "leave unchanged".
type UpdateDataUpstreamCircuitBreakerParams struct {
	ID               uuid.UUID
	AppID            uuid.UUID
	Enabled          *bool
	FailureThreshold *float64
	MinSamples       *int
	OpenSeconds      *int
}

// UpdateDataUpstreamCircuitBreaker (ADR-201 §3) — MemStore stub,
// Postgres-only, matching every other ADR-098 data_upstreams method.
func (m *MemStore) UpdateDataUpstreamCircuitBreaker(_ context.Context, _ UpdateDataUpstreamCircuitBreakerParams) error {
	return errMemStoreDataUpstreams
}

// UpdateDataUpstreamCircuitBreaker applies a partial policy update.
//
// Scoped by (id, app_id) rather than id alone: a forged ID from another app
// in the same account would otherwise let a customer enable a breaker on an
// upstream they can't see, and this rule can cut an app off from its
// database.
func (s *PgStore) UpdateDataUpstreamCircuitBreaker(ctx context.Context, in UpdateDataUpstreamCircuitBreakerParams) error {
	arg := sqlc.UpdateDataUpstreamCircuitBreakerParams{
		ID:    pgtype.UUID{Bytes: in.ID, Valid: true},
		AppID: pgtype.UUID{Bytes: in.AppID, Valid: true},
	}
	if in.Enabled != nil {
		arg.CircuitBreakerEnabled = pgtype.Bool{Bool: *in.Enabled, Valid: true}
	}
	if in.FailureThreshold != nil {
		arg.CircuitBreakerFailureThreshold = pgtype.Float8{Float64: *in.FailureThreshold, Valid: true}
	}
	if in.MinSamples != nil {
		arg.CircuitBreakerMinSamples = pgtype.Int4{Int32: int32(*in.MinSamples), Valid: true}
	}
	if in.OpenSeconds != nil {
		arg.CircuitBreakerOpenSeconds = pgtype.Int4{Int32: int32(*in.OpenSeconds), Valid: true}
	}
	if err := s.dataUpstreamsQueries().UpdateDataUpstreamCircuitBreaker(ctx, s.pool, arg); err != nil {
		return fmt.Errorf("state: update data_upstream circuit breaker: %w", err)
	}
	return nil
}
