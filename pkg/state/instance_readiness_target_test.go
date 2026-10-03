// adr: 375
package state

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type targetReadinessTestStore interface {
	AppendEventAt(context.Context, string, string, *string, []byte, time.Time) error
	LatestInstanceReadinessForTargets(context.Context, []ReadinessTarget) (map[string]map[string]InstanceReadiness, error)
}

func TestInstanceReadinessForTargetsBoundsBeforeDatabase(t *testing.T) {
	store := &PgStore{}
	for _, tc := range []struct {
		name    string
		targets []ReadinessTarget
		wantErr bool
	}{
		{"empty", nil, false},
		{"overflow", make([]ReadinessTarget, api.TrafficReadinessBatchSize+1), true},
		{"missing owner", []ReadinessTarget{{InstanceID: "instance", NodeID: "node"}}, true},
		{"missing node", []ReadinessTarget{{AppID: "app", InstanceID: "instance"}}, true},
		{"conflicting identities", []ReadinessTarget{{AppID: "app", InstanceID: "instance", NodeID: "node", WakeID: "one"}, {AppID: "app", InstanceID: "instance", NodeID: "node", WakeID: "two"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.LatestInstanceReadinessForTargets(t.Context(), tc.targets)
			if (err != nil) != tc.wantErr || len(got) != 0 {
				t.Fatalf("result=%+v err=%v", got, err)
			}
		})
	}
}

func TestInstanceReadinessForTargetsMemoryLifetimeOrdering(t *testing.T) {
	verifyTargetReadinessLifetimes(t, NewMemStore())
}
func TestInstanceReadinessForTargetsPostgresLifetimeOrdering(t *testing.T) {
	verifyTargetReadinessLifetimes(t, NewPgStore(pgtest.OpenMigrated(t)))
}

func verifyTargetReadinessLifetimes(t *testing.T, store targetReadinessTestStore) {
	t.Helper()
	// PostgreSQL persists timestamptz values at microsecond precision.
	at := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
	current := ReadinessTarget{AppID: "app", InstanceID: "instance", WakeID: "wake", NodeID: "node"}
	appendEvent := func(target ReadinessTarget, kind, source, status string, offset time.Duration) {
		t.Helper()
		body, err := json.Marshal(map[string]string{"app_id": target.AppID, "instance_id": target.InstanceID, "wake_id": target.WakeID, "node_id": target.NodeID, "sidecar_name": source, "status": status})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEventAt(t.Context(), "vmmd", kind, nil, body, at.Add(offset)); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent(current, "wake.app_readiness", "", "unready", 0)
	appendEvent(current, "wake.app_readiness", "", "ready", time.Second)
	appendEvent(current, "wake.sidecar_health", "proxy", "unready", time.Second)
	for _, identity := range []ReadinessTarget{
		{AppID: "other", InstanceID: "instance", WakeID: "wake", NodeID: "node"},
		{AppID: "app", InstanceID: "instance", WakeID: "old", NodeID: "node"},
		{AppID: "app", InstanceID: "instance", WakeID: "wake", NodeID: "old"},
		{AppID: "app", InstanceID: "instance"},
	} {
		appendEvent(identity, "wake.app_readiness", "", "unready", time.Hour)
		appendEvent(identity, "wake.sidecar_health", "proxy", "ready", time.Hour)
	}
	appendEvent(current, "wake.app_readiness", "", "unready", 0) // Delayed older observation.
	got, err := store.LatestInstanceReadinessForTargets(t.Context(), []ReadinessTarget{current, current, {AppID: "app", InstanceID: "missing", WakeID: "wake", NodeID: "node"}})
	if err != nil || len(got) != 1 || !got["instance"]["primary_app"].Ready || got["instance"]["sidecar:proxy"].Ready || len(got["instance"]) != 2 {
		t.Fatalf("scoped latest result=%+v err=%v", got, err)
	}
	if !got["instance"]["primary_app"].At.Equal(at.Add(time.Second)) {
		t.Fatal("retired observation masked current source")
	}
	legacy := current
	legacy.WakeID = ""
	got, err = store.LatestInstanceReadinessForTargets(t.Context(), []ReadinessTarget{legacy})
	if err != nil || got["instance"]["primary_app"].Ready || !got["instance"]["sidecar:proxy"].Ready {
		t.Fatalf("legacy result=%+v err=%v", got, err)
	}
}
