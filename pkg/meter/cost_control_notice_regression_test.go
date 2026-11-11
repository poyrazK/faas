package meter_test

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"strings"
	"testing"
	"time"
)

func TestCostControlSpendCapNoticeRetriesAfterMailFailure(t *testing.T) {
	store := state.NewMemStore()
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	store.SetClockForTest(func() time.Time { return now })
	account := makeAccount(t, t.Context(), store, api.PlanHobby)
	store.SetOverageCapCentsForTest(account.ID, 1)
	over := int64(api.PlanHobby.PlanIncludedGBHours()+2) * api.SecondsPerGBHour
	if err := store.AppendUsage(t.Context(), account.ID, "app-audit", "instance-audit", now.Add(-time.Minute), over, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	mailer := &fakeMailer{sendErr: errors.New("temporary mail outage")}
	cfg := &meter.Config{}
	cfg.Defaults()
	loop := meter.NewLoop(store, nil, &fakeParker{}, nil, &fakeNotifier{}, mailer, nil, nil, nil, func() time.Time { return now }, discardLog(), cfg, wire.NewOpsMetrics("meter_test_audit_retry"))
	loop.RunQuotaOnce(t.Context())
	mailer.mu.Lock()
	mailer.sendErr = nil
	mailer.mu.Unlock()
	now = now.Add(time.Minute)
	loop.RunQuotaOnce(t.Context())
	mailer.mu.Lock()
	defer mailer.mu.Unlock()
	notices := 0
	for _, message := range mailer.sent {
		if strings.Contains(message.Subject, "spend cap") {
			notices++
		}
	}
	if notices < 2 {
		t.Errorf("spend-cap delivery attempts=%d; failed first notice was never retried", notices)
	}
}
