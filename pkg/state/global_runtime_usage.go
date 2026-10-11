package state

// adr: 798
import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

// GlobalRuntimeUsageBinding comes from the directory's retained issued target,
// never from a customer or the cell's submitted billing account. Shared tenant
// billing remains unsupported until it has its own shared billing authority.
type GlobalRuntimeUsageBinding struct {
	Grant              runtimequota.Grant
	AccountID, AppSlug string
	ReleasedAt         *time.Time
}

// The portable adapter reads immutable issuance from the directory. PostgreSQL
// resolves it in the importing transaction. These reads never consult today's
// attached targets, registry origins, regional accounts, or owner plan.
type GlobalRuntimeUsageAuthority interface {
	GlobalRuntimeUsageBinding(context.Context, runtimequota.Grant) (GlobalRuntimeUsageBinding, error)
	GlobalRuntimeUsageObligations(context.Context, string, time.Time, time.Time) ([]GlobalRuntimeUsageBinding, error)
}

type GlobalRuntimeUsageReceipt struct {
	Usage                                 runtimequota.RuntimeUsage
	Envelope                              runtimequota.Envelope
	AccountID, AppSlug, BillingInstanceID string
	ImportedAt                            time.Time
}

type globalRuntimeUsageMutation struct {
	usage    runtimequota.RuntimeUsage
	envelope runtimequota.Envelope
}

type GlobalRuntimeUsageStore interface {
	applyGlobalRuntimeUsage(context.Context, globalRuntimeUsageMutation) (GlobalRuntimeUsageReceipt, error)
	GlobalRuntimeUsage(context.Context, string, string, string, time.Time) (GlobalRuntimeUsageReceipt, error)
	// Billing reads a full month prefix, including zero minutes. A missing cell
	// cannot reduce included usage or settle a provider's immutable hour receipt.
	GlobalRuntimeBillingReady(context.Context, string, time.Time, time.Time) (bool, error)
}

// ImportGlobalRuntimeUsage is the trusted meterd boundary. There is no customer
// billing-account argument and no unsigned mutation surface.
func ImportGlobalRuntimeUsage(ctx context.Context, store GlobalRuntimeUsageStore, cells runtimequota.Trust, e runtimequota.Envelope, now time.Time) (GlobalRuntimeUsageReceipt, error) {
	if store == nil {
		return GlobalRuntimeUsageReceipt{}, runtimequota.ErrTrust
	}
	u, err := cells.VerifyRuntimeUsage(e)
	if err != nil {
		return GlobalRuntimeUsageReceipt{}, err
	}
	if u.ObservedAtMicros > now.UTC().UnixMicro() {
		return GlobalRuntimeUsageReceipt{}, ErrInvalidArgument
	}
	return store.applyGlobalRuntimeUsage(ctx, globalRuntimeUsageMutation{usage: u, envelope: e})
}

func globalRuntimeBillingID(g runtimequota.Grant) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:global-runtime-usage:"+g.GlobalAppID+":"+g.AllocationID+":"+g.Binding.CellID)).String()
}

func globalRuntimeUsageKey(g runtimequota.Grant, minute time.Time) string {
	return globalRuntimeUsageRowKey(globalRuntimeBillingID(g), minute)
}

func globalRuntimeUsageRowKey(instanceID string, minute time.Time) string {
	return fmt.Sprintf("%s:%d", instanceID, minute.UTC().UnixMicro())
}

func globalRuntimeUsageWindow(b GlobalRuntimeUsageBinding, start, end time.Time) (time.Time, time.Time) {
	issued := time.UnixMicro(b.Grant.IssuedAtMicros).UTC().Truncate(time.Minute)
	if issued.After(start) {
		start = issued
	}
	if b.ReleasedAt != nil {
		closed := b.ReleasedAt.UTC().Truncate(time.Minute)
		if !closed.Equal(*b.ReleasedAt) {
			closed = closed.Add(time.Minute)
		}
		if closed.Before(end) {
			end = closed
		}
	}
	return start, end
}

func checkGlobalRuntimeUsageBinding(u runtimequota.RuntimeUsage, b GlobalRuntimeUsageBinding) error {
	accountID, err := uuid.Parse(b.AccountID)
	if b.Grant != u.Grant || err != nil || accountID == uuid.Nil || accountID.String() != u.Grant.OwnerID || !api.ValidGlobalAppSlug(b.AppSlug) || u.Grant.Binding.RegionalOrgID != "" {
		return ErrConflict
	}
	minute := time.UnixMicro(u.MinuteMicros).UTC()
	start, end := globalRuntimeUsageWindow(b, minute, minute.Add(time.Minute))
	if !start.Equal(minute) || !end.After(minute) {
		return ErrConflict
	}
	return nil
}
