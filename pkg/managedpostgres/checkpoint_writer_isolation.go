package managedpostgres

import (
	"context"
	"encoding/hex"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/checkpoint"
)

type CheckpointWriterInventory = checkpoint.WriterInventory

// CheckpointWriterIsolationRequest binds provider evidence to the original
// operation, physical placement, control-plane generation and closed catalog.
type CheckpointWriterIsolationRequest struct {
	Identity           CheckpointConnectionIdentity
	Definition         RestoreSourceDefinition
	Generation         int64
	Maintenance        CheckpointMaintenance
	ClosedAt           time.Time
	CatalogFingerprint string
}

func (CheckpointWriterIsolationRequest) String() string     { return "[private writer isolation request]" }
func (r CheckpointWriterIsolationRequest) GoString() string { return r.String() }

func (r CheckpointWriterIsolationRequest) Validate() error {
	digest, err := hex.DecodeString(r.CatalogFingerprint)
	if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != r.CatalogFingerprint ||
		r.Identity.Validate() != nil || r.Generation < 1 || r.Definition.Spec.Validate() != nil ||
		r.Definition.BackendID == "" || r.Definition.BackendFingerprint == "" || r.Definition.ProviderResourceID == "" ||
		r.Identity.SourceResourceID != r.Definition.DataResourceID || !ValidCheckpointConnectionRecovery(r.Maintenance, r.Identity) ||
		r.ClosedAt.IsZero() || r.ClosedAt.After(time.Now()) {
		return ErrInvalid
	}
	return nil
}

// CheckpointWriterIsolationEvidence is an attestation of a durable, operation-
// owned provider guard across every dataset endpoint. Expiry limits evidence;
// it must never release the guard. Only confirmed operation abandonment may do so.
type CheckpointWriterIsolationEvidence struct {
	Request                 CheckpointWriterIsolationRequest `json:"request"`
	Scope                   string                           `json:"scope"`
	CatalogStable           bool                             `json:"catalog_stable"`
	BackgroundWritersFenced bool                             `json:"background_writers_fenced"`
	ObservedAt              time.Time                        `json:"observed_at"`
	ExpiresAt               time.Time                        `json:"expires_at"`
}

func (CheckpointWriterIsolationEvidence) String() string {
	return "[private writer isolation evidence]"
}
func (e CheckpointWriterIsolationEvidence) GoString() string { return e.String() }
func (CheckpointWriterIsolationEvidence) MarshalJSON() ([]byte, error) {
	return []byte(`"[private writer isolation evidence]"`), nil
}

func (e CheckpointWriterIsolationEvidence) Validate(r CheckpointWriterIsolationRequest, now time.Time) error {
	if r.Validate() != nil || e.Request != r || e.Scope != "all_dataset_endpoints" ||
		!e.CatalogStable || !e.BackgroundWritersFenced || e.ObservedAt.Before(r.ClosedAt) || e.ObservedAt.After(now) ||
		e.ObservedAt.Nanosecond()%1000 != 0 || !e.ExpiresAt.After(e.ObservedAt) ||
		e.ExpiresAt.After(e.ObservedAt.Add(CutoverVerificationMaxAge)) {
		return ErrConflict
	}
	if !now.Before(e.ExpiresAt) {
		return ErrUnavailable
	}
	return nil
}

// CheckpointWriterIsolationProvider is optional and read-only. Implementations
// must install the durable guard as part of CloseCheckpointConnections, before
// acknowledging closure. It must prevent database creation/replacement and all
// background, replication, alternate-endpoint and provider-admin write paths.
// AbandonCheckpointConnections must release that same guard and permanently
// tombstone delayed close/guard requests before acknowledging its terminal state.
// SQL sampling, a stable fingerprint, capability flags or a worker lease alone
// cannot implement this contract. Unsupported guarantees return ErrUnsupported.
type CheckpointWriterIsolationProvider interface {
	ObserveCheckpointWriterIsolation(context.Context, CheckpointWriterIsolationRequest) (CheckpointWriterIsolationEvidence, error)
}

func (s *Service) ObserveCheckpointWriterIsolation(ctx context.Context, r CheckpointWriterIsolationRequest) (CheckpointWriterIsolationEvidence, error) {
	if err := ctx.Err(); err != nil {
		return CheckpointWriterIsolationEvidence{}, err
	}
	if r.Validate() != nil {
		return CheckpointWriterIsolationEvidence{}, ErrInvalid
	}
	b, err := s.checkpointBackend(r.Definition)
	if err != nil {
		return CheckpointWriterIsolationEvidence{}, err
	}
	p, ok := b.Provider.(CheckpointWriterIsolationProvider)
	if !ok {
		return CheckpointWriterIsolationEvidence{}, ErrUnsupported
	}
	probe, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	evidence, err := p.ObserveCheckpointWriterIsolation(probe, r)
	if err == nil {
		err = probe.Err()
	}
	if err == nil {
		err = evidence.Validate(r, s.now())
	}
	if err != nil {
		return CheckpointWriterIsolationEvidence{}, normalizeProviderError(err)
	}
	return evidence, nil
}
