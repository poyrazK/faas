package meter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// accountsUnavailable fails the billing account listing.
type accountsUnavailable struct {
	MemStore *state.MemStore
}

func (accountsUnavailable) ListAllAccounts(context.Context) ([]state.Account, error) {
	return nil, errors.New("accounts unavailable")
}

// The account list names each row's billing subject, so a roll that cannot
// read it writes nothing and fails; a later tick rolls the minute instead of
// billing a company app's usage to its creator for good.
// adr: 20261030120000
func TestSampler_AccountListFailureFailsTheRoll(t *testing.T) {
	mem, app, _, instID, minute := seedHobbyAppWithInstance(t, state.InstanceModeNormal)
	store := statetest.NewMem(accountsUnavailable{MemStore: mem})
	sampler := NewSampler(store, nil, func() time.Time { return minute })
	if _, err := sampler.SampleAndRoll(context.Background()); err == nil {
		t.Fatal("SampleAndRoll succeeded without the billing account list")
	}
	rows, err := mem.UsageByHour(context.Background(), app.AccountID, minute.Add(-time.Hour), minute.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.MBSeconds > 0 {
			t.Fatalf("usage written for instance %s while the billing subject was unknown: %+v", instID, row)
		}
	}
}
