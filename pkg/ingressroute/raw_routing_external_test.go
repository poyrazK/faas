// adr: 20261105115100
package ingressroute_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tcpd"
	"github.com/onebox-faas/faas/pkg/udpd"
)

func TestRawRoutingProductionScope(t *testing.T) {
	for _, mode := range []string{"production-only", "production-and-staging", "staging-only", "quarantined-staging"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			s := state.NewMemStore()
			account, err := s.CreateAccount(ctx, "audit-scope@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "network-audit"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, WorkloadName: "api", Slug: "audit-scope", Status: state.AppActive, RAMMB: 128, Manifest: state.AppManifest{Ports: []api.WorkloadPort{{Name: "tcp", Port: 9000}, {Name: "udp", Port: 5353, Protocol: api.WorkloadPortUDP}}}})
			if err != nil {
				t.Fatal(err)
			}
			tcp, err := s.CreateTCPListener(ctx, state.TCPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "tcp", PublicPort: 40401, GuestPort: 9000, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			udp, err := s.CreateUDPListener(ctx, state.UDPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "udp", PublicPort: 40400, GuestPort: 5353, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			create := func(id, scope string) {
				_, err := s.CreateDeployment(ctx, state.Deployment{ID: id, AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + id, Scope: scope, Status: state.DeployLive, TrafficPercent: 100})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.CreateInstance(ctx, app.ID, id, string(state.StateRunning), 128, "node", "wake-"+id); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "staging-only" {
				create("production", "production")
			}
			if mode != "production-only" {
				create("staging", "staging")
				if mode == "quarantined-staging" {
					if err := s.SetDeploymentParked(ctx, "staging", string(state.ParkReasonSecurityScanRegressed), time.Now()); err != nil {
						t.Fatal(err)
					}
				}
			}
			tr, te := (&tcpd.StoreTargetResolver{Instances: tcpd.NewCachedInstanceSource(s, time.Minute)}).ResolveTarget(ctx, tcpd.Route{AccountID: account.ID, AppID: app.ID, ListenerID: tcp.ID, ListenerName: "tcp", PublicPort: tcp.PublicPort, GuestPort: tcp.GuestPort, Protocol: "tcp"})
			ur, ue := (&udpd.StoreTargetResolver{Store: s}).ResolveTarget(ctx, udpd.Route{AccountID: account.ID, AppID: app.ID, ListenerID: udp.ID, ListenerName: "udp", PublicPort: udp.PublicPort, GuestPort: udp.GuestPort})
			if mode == "staging-only" {
				if te == nil || ue == nil {
					t.Fatalf("public ingress reached staging: TCP=%+v UDP=%+v", tr, ur)
				}
			} else if te != nil || ue != nil || tr.DeploymentID != "production" || ur.DeploymentID != "production" {
				t.Fatalf("production routing TCP=%+v/%v UDP=%+v/%v", tr, te, ur, ue)
			}
		})
	}
}

func TestRawReadinessWithdrawal(t *testing.T) {
	for _, probeStatus := range []string{"ready", "unready", "missing", "old-wake", "other-node", "withdrawn"} {
		t.Run(probeStatus, func(t *testing.T) {
			ctx := t.Context()
			s := state.NewMemStore()
			acct, err := s.CreateAccount(ctx, "audit-readiness@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "audit-ready", Status: state.AppActive, RAMMB: 128, Manifest: state.AppManifest{Ports: []api.WorkloadPort{{Name: "tcp", Port: 9000}, {Name: "udp", Port: 5353, Protocol: api.WorkloadPortUDP}}}})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := s.CreateDeployment(ctx, state.Deployment{ID: "ready-deployment", AppID: app.ID, Status: state.DeployLive, TrafficPercent: 100, OverrideReadinessProbe: json.RawMessage(`{"path":"/readyz"}`)})
			if err != nil {
				t.Fatal(err)
			}
			instance, err := s.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, "node", "current-wake")
			if err != nil {
				t.Fatal(err)
			}
			tcp, err := s.CreateTCPListener(ctx, state.TCPListener{AccountID: acct.ID, AppID: app.ID, ListenerName: "tcp", PublicPort: 40401, GuestPort: 9000, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			udp, err := s.CreateUDPListener(ctx, state.UDPListener{AccountID: acct.ID, AppID: app.ID, ListenerName: "udp", PublicPort: 40400, GuestPort: 5353, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			if probeStatus == "withdrawn" {
				body, _ := json.Marshal(map[string]string{"app_id": app.ID, "instance_id": instance.ID, "wake_id": instance.WakeID, "node_id": instance.NodeID, "status": "ready"})
				if err := s.AppendEventAt(ctx, "vmmd", "wake.app_readiness", nil, body, time.Now().Add(-2*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			if probeStatus != "missing" {
				body, _ := json.Marshal(map[string]string{"app_id": app.ID, "instance_id": instance.ID, "wake_id": instance.WakeID, "node_id": instance.NodeID, "status": probeStatus})
				if probeStatus == "withdrawn" {
					body, _ = json.Marshal(map[string]string{"app_id": app.ID, "instance_id": instance.ID, "wake_id": instance.WakeID, "node_id": instance.NodeID, "status": "unready"})
				}
				if probeStatus == "old-wake" || probeStatus == "other-node" {
					values := map[string]string{"app_id": app.ID, "instance_id": instance.ID, "wake_id": instance.WakeID, "node_id": instance.NodeID, "status": "ready"}
					if probeStatus == "old-wake" {
						values["wake_id"] = "old"
					} else {
						values["node_id"] = "other"
					}
					body, _ = json.Marshal(values)
				}
				if err := s.AppendEventAt(ctx, "vmmd", "wake.app_readiness", nil, body, time.Now().Add(-time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			target := gateway.Target{AppID: app.ID, InstanceID: instance.ID, DeploymentID: dep.ID, NodeID: instance.NodeID, WakeID: instance.WakeID, Port: 9000}
			backend := gateway.NewPGBackend(nil, nil, nil).WithTargetReadinessLoader(func(ctx context.Context, _ []gateway.Target) (map[string]gateway.TargetReadinessSnapshot, error) {
				observations, err := s.LatestInstanceReadinessForTargets(ctx, []state.ReadinessTarget{{AppID: app.ID, InstanceID: instance.ID, NodeID: instance.NodeID, WakeID: instance.WakeID}})
				snapshot := gateway.TargetReadinessSnapshot{AppID: app.ID, InstanceID: instance.ID, DeploymentID: dep.ID, NodeID: instance.NodeID, WakeID: instance.WakeID, RequiredSources: []string{"primary_app"}, States: map[string]gateway.ReadinessState{}}
				if observed, ok := observations[instance.ID]["primary_app"]; ok {
					snapshot.States["primary_app"] = gateway.ReadinessState{Ready: observed.Ready, UpdatedAt: observed.At, EventID: observed.EventID}
				}
				return map[string]gateway.TargetReadinessSnapshot{instance.ID: snapshot}, err
			})
			_, normalErr := backend.VerifyTargetReadiness(ctx, target)
			_, te := (&tcpd.StoreTargetResolver{Instances: s}).ResolveTarget(ctx, tcpd.Route{AccountID: acct.ID, AppID: app.ID, ListenerID: tcp.ID, ListenerName: "tcp", PublicPort: tcp.PublicPort, GuestPort: tcp.GuestPort, Protocol: "tcp"})
			_, ue := (&udpd.StoreTargetResolver{Store: s}).ResolveTarget(ctx, udpd.Route{AccountID: acct.ID, AppID: app.ID, ListenerID: udp.ID, ListenerName: "udp", PublicPort: udp.PublicPort, GuestPort: udp.GuestPort})
			t.Logf("probe=%s HTTP/private-TCP readiness=%v public-TCP=%v public-UDP=%v", probeStatus, normalErr, te, ue)
			if probeStatus == "ready" {
				if normalErr != nil || te != nil || ue != nil {
					t.Fatal("ready control failed")
				}
				return
			}
			if normalErr == nil {
				t.Fatal("reference readiness gate should refuse")
			}
			if te == nil || ue == nil {
				t.Errorf("raw ingress admitted instance with %s readiness: TCP=%v UDP=%v", probeStatus, te, ue)
			}
		})
	}
}
