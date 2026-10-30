//go:build !no_pg

package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A deleted app's broker triggers stay enabled for a restore but are not
// listed for polling: schedd kept draining the customer's broker into the
// dead app's dead letters (bug hunt 2026-10-28 B32).
func TestPgListEnabledTriggersSkipsDeletedApps(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	_, appID, _ := seedLiveDeploy(t, s, ctx, "-deleted-app-trigger")
	limits, _ := api.LimitsFor(api.PlanPro)
	kafka, err := s.CreateTriggerIfUnderQuota(ctx, appID, string(api.TriggerKindKafka), "orders", true,
		[]byte(`{"brokers":["b:9092"],"topic":"orders","group":"g"}`), "", 10, 1000, 3, 6*1024*1024, api.BrokerPoisonStrategyCommit, limits)
	if err != nil {
		t.Fatal(err)
	}
	listed := func() bool {
		t.Helper()
		triggers, err := s.ListEnabledTriggers(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, trigger := range triggers {
			if trigger.ID == kafka.ID {
				return true
			}
		}
		return false
	}
	if !listed() {
		t.Fatal("a live app's trigger is not listed")
	}
	if _, err := s.ScheduleAppDeletion(ctx, appID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if listed() {
		t.Fatal("a deleted app's trigger is still listed for polling")
	}
	if _, err := s.RestoreApp(ctx, appID, limits); err != nil {
		t.Fatal(err)
	}
	if !listed() {
		t.Fatal("a restored app's trigger did not come back")
	}
}
