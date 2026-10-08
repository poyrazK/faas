package globaldirectory

// adr: 786
import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func readIngressCatalog(ctx context.Context, db sqlc.DBTX) (IngressCatalog, error) {
	q := &sqlc.Queries{}
	apps, err := q.ListGlobalIngressApps(ctx, db, api.GlobalIngressRoutesMax+1)
	if err != nil {
		return nil, err
	}
	if len(apps) > api.GlobalIngressRoutesMax {
		return nil, ErrLimit
	}
	out := make(IngressCatalog, 0, len(apps))
	indices := map[string]int{}
	for _, a := range apps {
		indices[a.GlobalAppID] = len(out)
		out = append(out, IngressApp{App: App{ID: a.GlobalAppID, OwnerID: a.OwnerID, Slug: a.Slug, RoutingPolicy: a.RoutingPolicy, CreatedAt: a.CreatedAt.Time}, Candidates: []IngressCandidate{}})
	}
	rows, err := q.ListGlobalIngressCandidates(ctx, db, api.GlobalIngressRoutesMax*api.GlobalRuntimeQuotaTargetsMax+1)
	if err != nil {
		return nil, err
	}
	if len(rows) > api.GlobalIngressRoutesMax*api.GlobalRuntimeQuotaTargetsMax {
		return nil, ErrLimit
	}
	for _, r := range rows {
		var target Target
		var binding runtimequota.Binding
		if json.Unmarshal(r.Snapshot, &target) != nil || json.Unmarshal(r.Binding, &binding) != nil {
			return nil, ErrConflict
		}
		policy, ok := api.RuntimeQuotaPolicyForPlan(api.Plan(r.Plan))
		if !ok || policy.ID != r.PolicyID || policy.Concurrency != int(r.MaxConcurrency) || policy.PeakConcurrency != int(r.MaxPeakConcurrency) {
			return nil, ErrConflict
		}
		a := RuntimeQuotaAllocation{GlobalAppID: r.GlobalAppID, OwnerID: r.OwnerID, AllocationID: r.AllocationID, Generation: r.Generation, Policy: policy}
		t := RuntimeQuotaTarget{Target: target, Concurrency: int(r.Concurrency), RolloutConcurrency: int(r.RolloutConcurrency), IssuedAt: quotaTime(r.IssuedAt)}
		g, err := quotaWireGrant(r.IssuerID, a, t)
		if err != nil || g.Binding != binding {
			return nil, ErrConflict
		}
		c := runtimequota.Consent{ID: r.ConsentID, IssuerID: r.IssuerID, GlobalAppID: r.GlobalAppID, OwnerID: r.OwnerID, Binding: binding, ExpiresAtMicros: r.ExpiresAt.Time.UnixMicro()}
		i, ok := indices[r.GlobalAppID]
		if !ok {
			return nil, ErrConflict
		}
		out[i].Candidates = append(out[i].Candidates, IngressCandidate{Grant: g, Consent: c, Origin: api.RegionalAppOrigin(binding.RegionalAppSlug, r.AppsDomain)})
	}
	return canonicalIngressCatalog(out)
}
func (s *PgStore) IngressCatalog(ctx context.Context) (IngressCatalog, error) {
	var out IngressCatalog
	err := s.transaction(ctx, "read ingress catalog", func(tx pgx.Tx) error {
		if err := (&sqlc.Queries{}).LockGlobalIngressIntent(ctx, tx); err != nil {
			return err
		}
		var err error
		out, err = readIngressCatalog(ctx, tx)
		return err
	})
	return out, err
}
func (s *PgStore) CommitIngress(ctx context.Context, expected IngressCatalog, issuer string, sign func(int64) (runtimequota.Envelope, error)) error {
	return s.transaction(ctx, "commit ingress publication", func(tx pgx.Tx) error {
		q := &sqlc.Queries{}
		if err := q.LockGlobalIngressPublisher(ctx, tx); err != nil {
			return err
		}
		if err := q.LockGlobalIngressIntent(ctx, tx); err != nil {
			return err
		}
		id, err := q.ReadGlobalRuntimeQuotaIssuer(ctx, tx)
		if err != nil {
			return err
		}
		if id != issuer {
			return ErrConflict
		}
		current, err := readIngressCatalog(ctx, tx)
		if err != nil {
			return err
		}
		if !sameIngressCatalog(expected, current) {
			return ErrIngressChanged
		}
		previous, err := q.ReadGlobalIngressPublication(ctx, tx)
		generation := int64(1)
		if err == nil {
			if previous.IssuerID != issuer || previous.Generation >= api.GlobalRuntimeQuotaGenerationMax {
				return ErrConflict
			}
			generation = previous.Generation + 1
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		envelope, err := sign(generation)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(envelope)
		if err != nil || len(raw) > api.GlobalIngressEnvelopeMaxBytes {
			return runtimequota.ErrInvalid
		}
		if generation == 1 {
			return q.WriteGlobalIngressPublication(ctx, tx, sqlc.WriteGlobalIngressPublicationParams{IssuerID: idArg(issuer), Generation: generation, Envelope: raw})
		}
		count, err := q.UpdateGlobalIngressPublication(ctx, tx, sqlc.UpdateGlobalIngressPublicationParams{IssuerID: idArg(issuer), Generation: generation, Envelope: raw})
		if err == nil && count != 1 {
			return ErrConflict
		}
		return err
	})
}
func (s *PgStore) ExportIngress(ctx context.Context, export func([]byte) error) error {
	return s.transaction(ctx, "export ingress publication", func(tx pgx.Tx) error {
		q := &sqlc.Queries{}
		if err := q.LockGlobalIngressPublisher(ctx, tx); err != nil {
			return err
		}
		r, err := q.ReadGlobalIngressPublication(ctx, tx)
		if err != nil {
			return err
		}
		return export(r.Envelope)
	})
}

var _ IngressStore = (*PgStore)(nil)
