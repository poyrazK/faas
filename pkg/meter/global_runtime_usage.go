package meter

// adr: 798
import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

var ErrGlobalRuntimeUsageIncomplete = errors.New("global runtime usage coverage is incomplete")

// GlobalRuntimeUsageImporter runs in meterd. Trusted cells attest complete
// closed minutes, and the store atomically retains the first receipt and
// updates the canonical usage/financial ledgers consumed by normal billing.
type GlobalRuntimeUsageImporter struct {
	Store state.GlobalRuntimeUsageStore
	Cells runtimequota.Trust
	Now   func() time.Time
}

func (i GlobalRuntimeUsageImporter) Import(ctx context.Context, e runtimequota.Envelope) (state.GlobalRuntimeUsageReceipt, error) {
	now := time.Now()
	if i.Now != nil {
		now = i.Now()
	}
	return state.ImportGlobalRuntimeUsage(ctx, i.Store, i.Cells, e, now)
}

func (p *Pusher) globalRuntimeBillingComplete(ctx context.Context, account string, hour time.Time) error {
	month := AccountMonthKey(hour)
	ready, err := p.store.GlobalRuntimeBillingReady(ctx, account, month, hour.UTC().Add(time.Hour))
	if err != nil {
		return err
	}
	if !ready {
		return ErrGlobalRuntimeUsageIncomplete
	}
	return nil
}
