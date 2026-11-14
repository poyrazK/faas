// adr: 20261105115100
package udpd_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/ingressroute"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tcpd"
	"github.com/onebox-faas/faas/pkg/udpd"
)

func recordRawReadiness(t *testing.T, f rawOwnerFixture, instance state.Instance, ready bool) {
	t.Helper()
	status := "unready"
	if ready {
		status = "ready"
	}
	body, err := json.Marshal(map[string]string{"app_id": f.app.ID, "instance_id": instance.ID, "wake_id": instance.WakeID, "node_id": instance.NodeID, "status": status})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AppendEventAt(t.Context(), "vmmd", "wake.app_readiness", nil, body, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestRawIngressReadinessWarmFilteringAndWithdrawal(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp"} {
		t.Run(protocol, func(t *testing.T) {
			f := newRawOwnerFixture(t, true, true)
			rows, err := f.store.ListInstancesForApp(t.Context(), f.app.ID)
			if err != nil || len(rows) != 1 {
				t.Fatalf("instances=%v error=%v", rows, err)
			}
			recordRawReadiness(t, f, rows[0], false)
			healthy, err := f.store.CreateInstance(t.Context(), f.app.ID, "deployment", string(state.StateRunning), 256, "node", "healthy-wake")
			if err != nil {
				t.Fatal(err)
			}
			recordRawReadiness(t, f, healthy, true)
			admit := &rawOwnerAdmitter{}
			tcp := &tcpd.StoreTargetResolver{Instances: tcpd.NewCachedInstanceSource(f.store, time.Hour), Admitter: admit}
			udp := &udpd.StoreTargetResolver{Store: f.store, Admitter: admit}
			resolve := func() (gateway.Target, error) {
				if protocol == "tcp" {
					return tcp.ResolveTarget(t.Context(), f.tcp)
				}
				return udp.ResolveTarget(t.Context(), f.udp)
			}
			for range 4 {
				target, err := resolve()
				if err != nil || target.InstanceID != healthy.ID {
					t.Fatalf("target=%+v error=%v", target, err)
				}
			}
			recordRawReadiness(t, f, healthy, false)
			if _, err := resolve(); !errors.Is(err, ingressroute.ErrNoReadyInstance) {
				t.Fatalf("withdrawn cohort admitted: %v", err)
			}
			if admit.calls != 0 {
				t.Fatalf("unready residents triggered %d extra wakes", admit.calls)
			}
			recordRawReadiness(t, f, healthy, true)
			if target, err := resolve(); err != nil || target.InstanceID != healthy.ID {
				t.Fatalf("readiness recovery: %+v/%v", target, err)
			}
		})
	}
}

func TestRawIngressRechecksReadinessAfterWake(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp"} {
		for _, mode := range []string{"ready", "unready", "missing", "old-wake"} {
			t.Run(protocol+"/"+mode, func(t *testing.T) {
				f := newRawOwnerFixture(t, false, true)
				admit := &rawOwnerAdmitter{onAdmit: func() {
					if mode == "missing" {
						return
					}
					wake := "wake"
					if mode == "old-wake" {
						wake = "old"
					}
					recordRawReadiness(t, f, state.Instance{ID: "woken", NodeID: "node", WakeID: wake}, mode != "unready")
				}}
				var err error
				if protocol == "tcp" {
					_, err = (&tcpd.StoreTargetResolver{Instances: tcpd.NewCachedInstanceSource(f.store, time.Hour), Admitter: admit}).ResolveTarget(t.Context(), f.tcp)
				} else {
					_, err = (&udpd.StoreTargetResolver{Store: f.store, Admitter: admit}).ResolveTarget(t.Context(), f.udp)
				}
				if admit.calls != 1 || (err == nil) != (mode == "ready") {
					t.Fatalf("mode=%s calls=%d error=%v", mode, admit.calls, err)
				}
			})
		}
	}
}
