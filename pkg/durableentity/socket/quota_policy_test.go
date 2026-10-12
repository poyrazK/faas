package socket

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

type policyScanner struct {
	migrationScan
}

func (s *policyScanner) RaiseQuotaEntityLimit(context.Context, durableentity.ID, int64) error {
	return nil
}
func policyFixture(t *testing.T) (*Quotas, durableentity.ID, *quotaBucket, QuotaPolicyRequest) {
	t.Helper()
	q, id, b := quotaFixture()
	if err := q.AdmitInvocation(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	m, err := q.StartMigration(t.Context(), id, uuid.NewString(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	limits := q.Limits
	limits.EntityBytes *= 2
	limits.RetainedBytes *= 2
	p := QuotaPolicyRequest{OperationID: uuid.NewString(), ExpectedMigrationID: m.ID, ExpectedMigrationRevision: m.Revision, MigrationID: uuid.NewString(), Limits: limits}
	return q, id, b, p
}
func TestQuotaPolicyPreviewApplyReplayAndNodeFencing(t *testing.T) {
	q, id, b, p := policyFixture(t)
	older := *q
	q.Limits = p.Limits
	before := b.revision
	preview, err := q.PreviewPolicy(t.Context(), id, p)
	if err != nil || !preview.ReadyToApply || preview.AdditionalBytes != 1024 || b.revision != before {
		t.Fatal(preview, err)
	}
	change, err := q.ApplyPolicy(t.Context(), id, p, "operator")
	if err != nil || change.Revision != 1 {
		t.Fatal(change, err)
	}
	if _, err = older.AcquireConnection(t.Context(), id); !errors.Is(err, ErrPolicyMismatch) {
		t.Fatal("stale node reset policy", err)
	}
	if !errors.Is(q.AdmitInvocation(t.Context(), id), ErrMigrationPending) {
		t.Fatal("policy resumed before verification")
	}
	if _, err = q.StepMigration(t.Context(), id, p.ExpectedMigrationID, p.ExpectedMigrationRevision, &policyScanner{}); !errors.Is(err, durableentity.ErrConflict) {
		t.Fatal("old migration unfenced", err)
	}
	before = b.revision
	if replay, err := q.ApplyPolicy(t.Context(), id, p, "other-authorized-operator"); err != nil || replay.Revision != 1 || b.revision != before {
		t.Fatal(replay, err)
	}
	changed := p
	changed.Limits.InvocationsPerMinute++
	if _, err = q.ApplyPolicy(t.Context(), id, changed, "operator"); !errors.Is(err, durableentity.ErrRequestConflict) {
		t.Fatal("operation identity rebound", err)
	}
	status, err := q.PolicyStatus(t.Context(), id)
	if err != nil || status.Revision != 1 || len(status.Changes) != 1 || status.Changes[0].Actor != "operator" {
		t.Fatal(status, err)
	}
	now := time.Now()
	m, err := q.MigrationStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	now = m.NotBefore
	q.Now = func() time.Time { return now }
	scanner := &policyScanner{migrationScan: func(context.Context, string, durableentity.ID) (durableentity.QuotaInventoryPage, error) {
		e := migrationEntity(id, 10)
		e.StorageLimitBytes = p.Limits.EntityBytes
		return durableentity.QuotaInventoryPage{Entities: []durableentity.QuotaInventoryEntity{e}}, nil
	}}
	for m.Phase != "complete" {
		m, err = q.StepMigration(t.Context(), id, m.ID, m.Revision, scanner)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = q.AdmitInvocation(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	usage, err := older.Inspect(t.Context(), id)
	if err != nil || usage.Limits != p.Limits || usage.ReservedBytes != p.Limits.EntityBytes {
		t.Fatal("inspection clamped managed policy", usage, err)
	}
	if err = older.AdmitInvocation(t.Context(), id); !errors.Is(err, ErrPolicyMismatch) {
		t.Fatal(err)
	}
}
func TestQuotaPolicyRejectsUnsafeChangesAndHandlesUnknownCommit(t *testing.T) {
	for _, name := range []string{"decrease", "stale", "budget", "node", "uncertain"} {
		t.Run(name, func(t *testing.T) {
			q, id, b, p := policyFixture(t)
			q.Limits = p.Limits
			switch name {
			case "decrease":
				p.Limits.Connections--
			case "stale":
				p.ExpectedRevision = 1
			case "budget":
				// Reserve additional keys through the held record's private fixture,
				// representing a footprint that cannot fit proposed full entity caps.
				scope, _ := scopeFor(id)
				r, v, err := q.read(t.Context(), scopeKey(scope))
				if err != nil {
					t.Fatal(err)
				}
				other := id
				other.Key = "second"
				r.Entities = append(r.Entities, entityReservation{entityKey(other), 1024})
				body, _ := json.Marshal(r)
				if _, err = b.Put(t.Context(), "tenant-quotas/v1/"+scopeKey(scope), body, v); err != nil {
					t.Fatal(err)
				}
				p.Limits.EntityBytes = 1 << 20
				p.Limits.RetainedBytes = 1 << 20
				q.Limits = p.Limits
			case "node":
				q.Limits.EntityBytes--
			case "uncertain":
				b.uncertain = true
			}
			before := b.revision
			result, err := q.ApplyPolicy(t.Context(), id, p, "operator")
			if name == "uncertain" {
				if result != nil || !errors.Is(err, durableentity.ErrUncertain) || b.revision != before+1 {
					t.Fatal(result, err)
				}
				status, err := q.PolicyStatus(t.Context(), id)
				if err != nil || status.Revision != 1 {
					t.Fatal(status, err)
				}
				if _, err = q.ApplyPolicy(t.Context(), id, p, "operator"); err != nil {
					t.Fatal(err)
				}
			} else if err == nil || b.revision != before {
				t.Fatal("unsafe policy applied", result, err)
			}
		})
	}
}
