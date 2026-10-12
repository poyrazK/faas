package socket

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

type migrationScan func(context.Context, string, durableentity.ID) (durableentity.QuotaInventoryPage, error)

func (f migrationScan) ScanQuotaInventory(ctx context.Context, cursor string, id durableentity.ID) (durableentity.QuotaInventoryPage, error) {
	return f(ctx, cursor, id)
}
func migrationEntity(id durableentity.ID, n int64) durableentity.QuotaInventoryEntity {
	return durableentity.QuotaInventoryEntity{Entity: id, RetainedBytes: &n}
}

func TestQuotaMigrationPlanResumeVerifyAndFencing(t *testing.T) {
	q, id, bucket := quotaFixture()
	now := time.Now()
	q.Now = func() time.Time { return now }
	second := id
	second.Key = "second"
	scanner := migrationScan(func(_ context.Context, cursor string, _ durableentity.ID) (durableentity.QuotaInventoryPage, error) {
		if cursor == "" {
			return durableentity.QuotaInventoryPage{Entities: []durableentity.QuotaInventoryEntity{migrationEntity(id, 10)}, NextCursor: "page2"}, nil
		}
		if cursor != "page2" {
			t.Fatal(cursor)
		}
		return durableentity.QuotaInventoryPage{Entities: []durableentity.QuotaInventoryEntity{migrationEntity(second, 20)}}, nil
	})
	plan, err := q.PlanMigration(t.Context(), id, "", scanner)
	if err != nil || !plan.PageFits || plan.MissingReservations != 1 || plan.AdditionalBytes != 1024 || bucket.revision != 0 {
		t.Fatal(plan, err, bucket.revision)
	}
	m, err := q.StartMigration(t.Context(), id, uuid.NewString(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(q.AdmitInvocation(t.Context(), id), ErrMigrationPending) {
		t.Fatal("migration did not hold invocation")
	}
	if _, err = q.AcquireConnection(t.Context(), id); !errors.Is(err, ErrMigrationPending) {
		t.Fatal("migration admitted connection", err)
	}
	if _, _, err = (&Grants{Store: bucket, Quotas: q}).Mint(t.Context(), testGrant(id), time.Minute); !errors.Is(err, ErrMigrationPending) {
		t.Fatal("migration minted grant", err)
	}
	if _, err = q.StepMigration(t.Context(), id, m.ID, m.Revision, scanner); !errors.Is(err, durableentity.ErrBusy) {
		t.Fatal("drain wait bypassed", err)
	}
	now = m.NotBefore
	first, err := q.StepMigration(t.Context(), id, m.ID, m.Revision, scanner)
	if err != nil || first.Cursor != "page2" {
		t.Fatal(first, err)
	}
	if _, err = q.StepMigration(t.Context(), id, m.ID, m.Revision, scanner); !errors.Is(err, durableentity.ErrConflict) {
		t.Fatal("stale revision advanced", err)
	}
	restarted := *q
	for first.Phase != "complete" {
		first, err = restarted.StepMigration(t.Context(), id, first.ID, first.Revision, scanner)
		if err != nil {
			t.Fatal(err)
		}
	}
	if first.Observed != 2 {
		t.Fatal(first)
	}
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.EntityReservations != 2 || usage.ReservedBytes != 2048 || usage.Invocations != 0 || usage.GrantsIssued != 0 {
		t.Fatal(usage, err)
	}
	old := *first
	replacement, err := q.StartMigration(t.Context(), id, uuid.NewString(), old.ID, old.Revision)
	if err != nil || replacement.ID == old.ID {
		t.Fatal(replacement, err)
	}
	if _, err = q.StepMigration(t.Context(), id, old.ID, old.Revision, scanner); !errors.Is(err, durableentity.ErrConflict) {
		t.Fatal("replaced worker advanced", err)
	}
}

func TestQuotaMigrationBlockedPagesAndUnknownWrites(t *testing.T) {
	for _, name := range []string{"unknown_usage", "above_cap", "budget", "wrong_scope", "uncertain", "verification_gap"} {
		t.Run(name, func(t *testing.T) {
			q, id, bucket := quotaFixture()
			now := time.Now()
			q.Now = func() time.Time { return now }
			m, err := q.StartMigration(t.Context(), id, uuid.NewString(), "", 0)
			if err != nil {
				t.Fatal(err)
			}
			now = m.NotBefore
			entry := migrationEntity(id, 10)
			if name == "unknown_usage" {
				entry.RetainedBytes = nil
			}
			if name == "above_cap" {
				entry = migrationEntity(id, 2048)
			}
			if name == "wrong_scope" {
				entry.Entity.TenantID = "other"
			}
			entries := []durableentity.QuotaInventoryEntity{entry}
			if name == "budget" {
				q.Limits.RetainedBytes = q.Limits.EntityBytes
				another := entry
				another.Entity.Key = "another"
				entries = append(entries, another)
				m, err = q.StartMigration(t.Context(), id, uuid.NewString(), m.ID, m.Revision)
				if err != nil {
					t.Fatal(err)
				}
				now = m.NotBefore
			}
			scanner := migrationScan(func(context.Context, string, durableentity.ID) (durableentity.QuotaInventoryPage, error) {
				return durableentity.QuotaInventoryPage{Entities: entries}, nil
			})
			plan, err := q.PlanMigration(t.Context(), id, "", scanner)
			if name == "wrong_scope" {
				if !errors.Is(err, durableentity.ErrCorrupt) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			before := bucket.revision
			if name == "uncertain" {
				bucket.uncertain = true
			}
			next, stepErr := q.StepMigration(t.Context(), id, m.ID, m.Revision, scanner)
			switch name {
			case "uncertain":
				if next != nil || !errors.Is(stepErr, durableentity.ErrUncertain) || bucket.revision != before+1 {
					t.Fatal(next, stepErr)
				}
				status, err := q.MigrationStatus(t.Context(), id)
				if err != nil || status.Phase != "verify" {
					t.Fatal(status, err)
				}
			case "verification_gap":
				if stepErr != nil {
					t.Fatal(stepErr)
				}
				entries[0].Entity.Key = "new_unreserved"
				if _, err = q.StepMigration(t.Context(), id, next.ID, next.Revision, scanner); !errors.Is(err, durableentity.ErrInventoryPending) {
					t.Fatal("verification missed gap", err)
				}
			default:
				if plan.PageFits || stepErr == nil || bucket.revision != before {
					t.Fatal(plan, stepErr, bucket.revision, before)
				}
			}
		})
	}
}

func TestQuotaMigrationUncertainStartAndConcurrentReplacement(t *testing.T) {
	q, id, bucket := quotaFixture()
	now := time.Now()
	q.Now = func() time.Time { return now }
	lease, err := q.AcquireConnection(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.StartMigration(t.Context(), id, uuid.NewString(), "", 0); !errors.Is(err, durableentity.ErrBusy) {
		t.Fatal("live connection was not drained", err)
	}
	if err = q.ReleaseConnection(t.Context(), id, lease); err != nil {
		t.Fatal(err)
	}
	migrationID := uuid.NewString()
	bucket.uncertain = true
	if m, err := q.StartMigration(t.Context(), id, migrationID, "", 0); m != nil || !errors.Is(err, durableentity.ErrUncertain) {
		t.Fatal(m, err)
	}
	m, err := q.MigrationStatus(t.Context(), id)
	if err != nil || m == nil || m.ID != migrationID {
		t.Fatal(m, err)
	}
	now = m.NotBefore
	scanner := migrationScan(func(ctx context.Context, _ string, _ durableentity.ID) (durableentity.QuotaInventoryPage, error) {
		if _, err := q.StartMigration(ctx, id, uuid.NewString(), m.ID, m.Revision); err != nil {
			t.Fatal(err)
		}
		return durableentity.QuotaInventoryPage{Entities: []durableentity.QuotaInventoryEntity{migrationEntity(id, 10)}}, nil
	})
	if _, err = q.StepMigration(t.Context(), id, m.ID, m.Revision, scanner); !errors.Is(err, durableentity.ErrConflict) {
		t.Fatal("stale scan published", err)
	}
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.EntityReservations != 0 {
		t.Fatal("replaced scan charged reservations", usage, err)
	}
}
