// adr: 20261105120000
package udpd_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tcpd"
	"github.com/onebox-faas/faas/pkg/udpd"
)

func TestRawIngressRefusesQuarantinedDeployment(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp"} {
		t.Run(protocol, func(t *testing.T) {
			f := newRawOwnerFixture(t, true)
			tcp := &tcpd.StoreTargetResolver{Instances: tcpd.NewCachedInstanceSource(f.store, time.Hour)}
			if _, err := tcp.ResolveTarget(t.Context(), f.tcp); err != nil {
				t.Fatal(err)
			}
			if err := f.store.SetDeploymentParked(t.Context(), "deployment", string(state.ParkReasonSecurityScanRegressed), time.Now()); err != nil {
				t.Fatal(err)
			}
			parked := state.AppEvictedCold
			if _, err := f.store.UpdateApp(t.Context(), f.app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
				t.Fatal(err)
			}
			if protocol == "tcp" {
				target, err := tcp.ResolveTarget(t.Context(), f.tcp)
				if err == nil {
					t.Fatalf("TCP admitted security-quarantined deployment: %+v", target)
				}
			} else {
				target, err := (&udpd.StoreTargetResolver{Store: f.store}).ResolveTarget(t.Context(), f.udp)
				if err == nil {
					t.Fatalf("UDP admitted security-quarantined deployment: %+v", target)
				}
			}
		})
	}
}

func TestQuarantineWithdrawsPublicListenerInventory(t *testing.T) {
	f := newRawOwnerFixture(t, true)
	if err := f.store.SetDeploymentParked(t.Context(), "deployment", string(state.ParkReasonSecurityScanRegressed), time.Now()); err != nil {
		t.Fatal(err)
	}
	parked := state.AppEvictedCold
	if _, err := f.store.UpdateApp(t.Context(), f.app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatal(err)
	}
	tcp, err := f.store.ListEnabledTCPListeners(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	udp, err := f.store.ListEnabledUDPListeners(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tcp) != 0 || len(udp) != 0 {
		t.Fatalf("quarantine left public listener inventories enabled: TCP=%d UDP=%d", len(tcp), len(udp))
	}
}

func TestRawIngressRechecksQuarantineAfterSelectionAndWake(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp"} {
		for _, running := range []bool{false, true} {
			t.Run(protocol+"/running="+fmt.Sprint(running), func(t *testing.T) {
				f := newRawOwnerFixture(t, running)
				quarantine := func() {
					if err := f.store.SetDeploymentParked(t.Context(), "deployment", string(state.ParkReasonSecurityScanRegressed), time.Now()); err != nil {
						t.Error(err)
					}
				}
				source := rawSelectionSource{InstanceSource: f.store, udp: f.store, onList: func() {}}
				admit := &rawOwnerAdmitter{onAdmit: quarantine}
				if running {
					source.onList = quarantine
				}
				var err error
				if protocol == "tcp" {
					_, err = (&tcpd.StoreTargetResolver{Instances: source, Admitter: admit}).ResolveTarget(t.Context(), f.tcp)
				} else {
					_, err = (&udpd.StoreTargetResolver{Store: source, Admitter: admit}).ResolveTarget(t.Context(), f.udp)
				}
				if err == nil {
					t.Fatal("quarantine committed during selection admitted a target")
				}
			})
		}
	}
}

func TestQuarantineRetainsIntentAndCleanDeploymentRestoresListeners(t *testing.T) {
	f := newRawOwnerFixture(t, true)
	if err := f.store.SetDeploymentParked(t.Context(), "deployment", string(state.ParkReasonSecurityScanRegressed), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.TCPListenerByPublicPort(t.Context(), f.tcp.PublicPort); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("public TCP lookup: %v", err)
	}
	if _, err := f.store.UDPListenerByPublicPort(t.Context(), f.udp.PublicPort); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("public UDP lookup: %v", err)
	}
	if row, err := f.store.TCPListenerByAppAndName(t.Context(), f.app.ID, "tcp"); err != nil || !row.Enabled {
		t.Fatalf("TCP intent lost: %+v %v", row, err)
	}
	if row, err := f.store.UDPListenerByAppAndName(t.Context(), f.app.ID, "udp"); err != nil || !row.Enabled {
		t.Fatalf("UDP intent lost: %+v %v", row, err)
	}
	clean, err := f.store.CreateDeployment(t.Context(), state.Deployment{AppID: f.app.ID, Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkDeploymentLive(t.Context(), clean.ID); err != nil {
		t.Fatal(err)
	}
	tcp, err := f.store.ListEnabledTCPListeners(t.Context())
	if err != nil || len(tcp) != 1 {
		t.Fatalf("TCP recovery: %v %v", tcp, err)
	}
	udp, err := f.store.ListEnabledUDPListeners(t.Context())
	if err != nil || len(udp) != 1 {
		t.Fatalf("UDP recovery: %v %v", udp, err)
	}
}
