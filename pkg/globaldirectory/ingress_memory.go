package globaldirectory

// adr: 658
import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func (s *MemoryStore) ingressCatalog(ctx context.Context) (IngressCatalog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(IngressCatalog, 0, len(s.apps))
	for _, app := range s.apps {
		item := IngressApp{App: app, Candidates: []IngressCandidate{}}
		out = append(out, item)
		owner := s.owners[app.OwnerID]
		if owner.Shared() {
			continue
		} // Shared execution/billing authority remains unavailable.
		policy, err := s.currentQuotaPolicy(ctx, app.OwnerID)
		if errors.Is(err, ErrOwnerInactive) || errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, a := range s.runtimeQuotas[app.ID] {
			if a.Generation != s.runtimeQuotaHeads[app.ID] || a.Policy != policy || a.RetiredAt != nil {
				continue
			}
			for _, t := range a.Targets {
				current, ok := s.delegationTarget(app.ID, t.Region)
				cell := s.cells[t.Region]
				if !ok || !snapshotMatches(t.Target, current) || t.IssuedAt == nil || t.ReleasedAt != nil || cell.AppsDomain == "" {
					continue
				}
				grant, err := quotaWireGrant(s.runtimeQuotaIssuer, a, t)
				if err != nil {
					return nil, err
				}
				var consent runtimequota.Consent
				for _, r := range s.runtimeConsents {
					if r.Status == RuntimeConsentActive && r.Consent.Matches(grant) && r.Consent.ExpiresAtMicros > time.Now().UnixMicro() && (consent.ID == "" || r.Consent.ID < consent.ID) {
						consent = r.Consent
					}
				}
				if consent.ID != "" {
					out[len(out)-1].Candidates = append(out[len(out)-1].Candidates, IngressCandidate{Grant: grant, Consent: consent, Origin: api.RegionalAppOrigin(t.RegionalAppSlug, cell.AppsDomain)})
				}
			}
		}
	}
	return canonicalIngressCatalog(out)
}
func (s *MemoryStore) IngressCatalog(ctx context.Context) (IngressCatalog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ingressCatalog(ctx)
}
func (s *MemoryStore) CommitIngress(ctx context.Context, expected IngressCatalog, issuer string, sign func(int64) (runtimequota.Envelope, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if issuer == "" || issuer != s.runtimeQuotaIssuer || s.ingressGeneration >= api.GlobalRuntimeQuotaGenerationMax {
		return ErrConflict
	}
	current, err := s.ingressCatalog(ctx)
	if err != nil {
		return err
	}
	if !sameIngressCatalog(expected, current) {
		return ErrIngressChanged
	}
	envelope, err := sign(s.ingressGeneration + 1)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(envelope)
	if err != nil || len(raw) > api.GlobalIngressEnvelopeMaxBytes {
		return runtimequota.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.ingressGeneration++
	s.ingressEnvelope = append([]byte(nil), raw...)
	return nil
}
func (s *MemoryStore) ExportIngress(ctx context.Context, export func([]byte) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(s.ingressEnvelope) == 0 {
		return ErrNotFound
	}
	return export(append([]byte(nil), s.ingressEnvelope...))
}

var _ IngressStore = (*MemoryStore)(nil)
