package socket

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

type retirementReader struct {
	status *durableentity.RetirementStatus
	err    error
}

func (r retirementReader) RetirementStatus(context.Context, durableentity.ID) (*durableentity.RetirementStatus, error) {
	return r.status, r.err
}
func retirementQuotaFixture(t *testing.T) (*Quotas, durableentity.ID, *quotaBucket, retirementReader) {
	t.Helper()
	q, id, b, p := policyFixture(t)
	p.Limits = q.Limits // Adopt legacy caps into managed policy without increasing.
	if _, err := q.ApplyPolicy(t.Context(), id, p, "operator"); err != nil {
		t.Fatal(err)
	}
	m, err := q.MigrationStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	now := m.NotBefore
	q.Now = func() time.Time { return now }
	scan := &policyScanner{migrationScan: func(context.Context, string, durableentity.ID) (durableentity.QuotaInventoryPage, error) {
		e := migrationEntity(id, 10)
		e.StorageLimitBytes = q.Limits.EntityBytes
		return durableentity.QuotaInventoryPage{Entities: []durableentity.QuotaInventoryEntity{e}}, nil
	}}
	for m.Phase != "complete" {
		m, err = q.StepMigration(t.Context(), id, m.ID, m.Revision, scan)
		if err != nil {
			t.Fatal(err)
		}
	}
	reader := retirementReader{status: &durableentity.RetirementStatus{Request: durableentity.RetirementRequest{OperationID: uuid.NewString()}, Actor: "operator", At: now, Revision: 1, Phase: "cleanup"}}
	return q, id, b, reader
}
func TestQuotaRetirementReclaimsSlotChargesRecoveryAndPermanentlyFencesAdmission(t *testing.T) {
	q, id, _, reader := retirementQuotaFixture(t)
	preview, err := q.PreviewRetirement(t.Context(), id)
	if err != nil || !preview.Ready || preview.ReservedBytes != 1024 {
		t.Fatal(preview, err)
	}
	op := reader.status.Request.OperationID
	if _, err = q.TrackRetirement(t.Context(), id, op, reader); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReclaimRetirement(t.Context(), id, op, reader); !errors.Is(err, durableentity.ErrInventoryPending) {
		t.Fatal("unverified bytes reclaimed", err)
	}
	if _, err = q.StartMigration(t.Context(), id, uuid.NewString(), "", 0); !errors.Is(err, durableentity.ErrInventoryPending) {
		t.Fatal("migration raced retirement", err)
	}
	reader.status.Phase, reader.status.RetainedBytes = "complete", 128
	receipt, err := q.ReclaimRetirement(t.Context(), id, op, reader)
	if err != nil || receipt.ReleasedBytes != 896 {
		t.Fatal(receipt, err)
	}
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.EntityReservations != 0 || usage.ReservedBytes != 128 || usage.RecoveryBytes != 128 || usage.RetiredEntities != 1 {
		t.Fatal(usage, err)
	}
	if replay, err := q.ReclaimRetirement(t.Context(), id, op, reader); err != nil || *replay != *receipt {
		t.Fatal(replay, err)
	}
	if err = q.AdmitInvocation(t.Context(), id); !errors.Is(err, durableentity.ErrRetired) {
		t.Fatal("retired entity admitted", err)
	}
	if _, err = q.AcquireConnection(t.Context(), id); !errors.Is(err, durableentity.ErrRetired) {
		t.Fatal("retired connection admitted", err)
	}
	g := testGrant(id)
	g.ID = scopeKey(quotaScope{id.AccountID, id.AppID, id.EnvironmentID, id.TenantID}) + "." + uuid.NewString()
	g.ExpiresAt = q.now().Add(time.Minute)
	if _, err = q.mint(t.Context(), g); !errors.Is(err, durableentity.ErrRetired) {
		t.Fatal("retired grant admitted", err)
	}
	fresh := id
	fresh.Key = "new-document"
	if err = q.AdmitInvocation(t.Context(), fresh); err != nil {
		t.Fatal("fresh identity cannot use capacity", err)
	}
}
func TestQuotaRetirementUnknownReclamationCannotCreditTwice(t *testing.T) {
	q, id, b, reader := retirementQuotaFixture(t)
	op := reader.status.Request.OperationID
	if _, err := q.TrackRetirement(t.Context(), id, op, reader); err != nil {
		t.Fatal(err)
	}
	reader.status.Phase, reader.status.RetainedBytes = "complete", 128
	b.uncertain = true
	if receipt, err := q.ReclaimRetirement(t.Context(), id, op, reader); receipt != nil || !errors.Is(err, durableentity.ErrUncertain) {
		t.Fatal(receipt, err)
	}
	receipt, err := q.ReclaimRetirement(t.Context(), id, op, reader)
	if err != nil || receipt.ReleasedBytes != 896 {
		t.Fatal(receipt, err)
	}
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.ReservedBytes != 128 {
		t.Fatal(usage, err)
	}
	if _, err = q.ReclaimRetirement(t.Context(), id, uuid.NewString(), reader); !errors.Is(err, durableentity.ErrRequestConflict) {
		t.Fatal("identity rebound", err)
	}
}
