// adr: 375
package gateway

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTargetPublicationUnknownConfigurationCannotRoute(t *testing.T) {
	target := Target{AppID: "app", InstanceID: "instance", DeploymentID: "deployment", NodeID: "node", WakeID: "wake"}
	at := time.Now().UTC()
	snapshot := TargetReadinessSnapshot{AppID: target.AppID, InstanceID: target.InstanceID, DeploymentID: target.DeploymentID,
		NodeID: target.NodeID, WakeID: target.WakeID, RequiredSources: []string{"primary_app", "sidecar:proxy"},
		States: map[string]ReadinessState{"primary_app": {Ready: true, UpdatedAt: at, EventID: 1}, "sidecar:proxy": {UpdatedAt: at, EventID: 2}}}
	reads := 0
	var readErr error
	b := NewPGBackend(nil, nil, nil).WithTargetReadinessLoader(func(context.Context, []Target) (map[string]TargetReadinessSnapshot, error) {
		reads++
		return map[string]TargetReadinessSnapshot{target.InstanceID: snapshot}, readErr
	})
	assert := func(ready bool) {
		t.Helper()
		endpoints, err := b.ServiceEndpoints(t.Context(), target.AppID)
		if err != nil {
			t.Fatal(err)
		}
		endpoint := ServiceEndpoint{InstanceID: target.InstanceID, DeploymentID: target.DeploymentID, NodeID: target.NodeID, Port: 8080, wakeID: target.WakeID}
		if b.Pick(target.AppID).OK != ready || b.ServiceEndpointRoutable(target.AppID, endpoint) != ready ||
			(len(endpoints.Endpoints) > 0) != ready || b.CapacityCount(target.AppID) != 1 {
			t.Fatalf("publication readiness: want=%t pick=%+v endpoints=%+v capacity=%d", ready, b.Pick(target.AppID), endpoints, b.CapacityCount(target.AppID))
		}
	}
	b.RecordTarget(target.AppID, target)
	assert(false)
	if reads != 0 {
		t.Fatal("bare cache publication performed an unbounded synchronous read")
	}
	// One early probe notification cannot certify the complete source set.
	b.SetInstanceReadinessForTarget(target.AppID, target.InstanceID, target.WakeID, target.NodeID, "primary_app", "ready", at.Add(time.Second), 3)
	assert(false)
	if err := b.ReconcileTargetReadiness(t.Context()); err != nil || reads != 1 {
		t.Fatalf("unknown configuration was not discovered: reads=%d err=%v", reads, err)
	}
	assert(false)
	snapshot.States["sidecar:proxy"] = ReadinessState{Ready: true, UpdatedAt: at.Add(2 * time.Second), EventID: 4}
	if err := b.ReconcileTargetReadiness(t.Context()); err != nil {
		t.Fatal(err)
	}
	assert(true)
	lease := b.Pick(target.AppID).Target.ReadinessVerifiedUntil
	b.RecordTarget(target.AppID, target)
	assert(true)
	if b.Pick(target.AppID).Target.ReadinessVerifiedUntil != lease {
		t.Fatal("bare replay renewed readiness verification")
	}
	readErr = errors.New("store unavailable")
	if err := b.ReconcileTargetReadiness(t.Context()); !errors.Is(err, readErr) {
		t.Fatalf("store failure lost: %v", err)
	}
	assert(false)
	b.RecordTarget(target.AppID, target)
	assert(false)
}

func TestTargetPublicationDisabledProbeRequiresConfigurationFirst(t *testing.T) {
	target := Target{AppID: "app", InstanceID: "instance", DeploymentID: "deployment", NodeID: "node", WakeID: "wake"}
	reads := 0
	b := NewPGBackend(nil, nil, nil).WithTargetReadinessLoader(func(context.Context, []Target) (map[string]TargetReadinessSnapshot, error) {
		reads++
		return map[string]TargetReadinessSnapshot{target.InstanceID: {AppID: target.AppID, InstanceID: target.InstanceID,
			DeploymentID: target.DeploymentID, NodeID: target.NodeID, WakeID: target.WakeID}}, nil
	})
	b.RecordTarget(target.AppID, target)
	if b.Pick(target.AppID).OK || b.CapacityCount(target.AppID) != 1 {
		t.Fatal("absence of configuration was treated as a disabled probe")
	}
	for range 2 {
		if err := b.ReconcileTargetReadiness(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if !b.Pick(target.AppID).OK || reads != 1 {
		t.Fatalf("verified disabled probe did not route independently: reads=%d pick=%+v", reads, b.Pick(target.AppID))
	}
	// A replacement lifetime must discover configuration independently.
	target.WakeID = "replacement"
	b.RecordTarget(target.AppID, target)
	if b.Pick(target.AppID).OK || b.CapacityCount(target.AppID) != 1 {
		t.Fatal("replacement inherited readiness from a retired lifetime")
	}
}

func TestTargetPublicationCertificateCannotFollowMutatedIdentity(t *testing.T) {
	for _, field := range []string{"app", "instance", "deployment", "node", "wake", "port", "default port"} {
		t.Run(field, func(t *testing.T) {
			target := Target{AppID: "app", InstanceID: "instance", DeploymentID: "deployment", NodeID: "node", WakeID: "wake"}
			b := NewPGBackend(nil, nil, nil).WithTargetReadinessLoader(func(context.Context, []Target) (map[string]TargetReadinessSnapshot, error) {
				return map[string]TargetReadinessSnapshot{target.InstanceID: {AppID: target.AppID, InstanceID: target.InstanceID, DeploymentID: target.DeploymentID, NodeID: target.NodeID, WakeID: target.WakeID}}, nil
			})
			if err := b.RecordTargetWithReadiness(t.Context(), target.AppID, target); err != nil {
				t.Fatal(err)
			}
			copy := b.Pick(target.AppID).Target
			switch field {
			case "app":
				copy.AppID = "other"
			case "instance":
				copy.InstanceID = "other"
			case "deployment":
				copy.DeploymentID = "other"
			case "node":
				copy.NodeID = "other"
			case "wake":
				copy.WakeID = "other"
			case "port":
				copy.Port = 9090
			case "default port":
				copy.Port = 8080
			}
			if copy.routeReady() != (field == "default port") {
				t.Fatal("readiness certificate followed a different routing identity")
			}
		})
	}
}

func TestTargetPublicationNotificationDuringConfigurationReadCannotBeMasked(t *testing.T) {
	for _, path := range []string{"repair", "direct forwarding"} {
		t.Run(path, func(t *testing.T) {
			target := Target{AppID: "app", InstanceID: "instance", DeploymentID: "deployment", NodeID: "node", WakeID: "wake"}
			at := time.Now().UTC()
			snapshot := TargetReadinessSnapshot{AppID: target.AppID, InstanceID: target.InstanceID, DeploymentID: target.DeploymentID, NodeID: target.NodeID, WakeID: target.WakeID,
				RequiredSources: []string{"sidecar:proxy"}, States: map[string]ReadinessState{"sidecar:proxy": {Ready: true, UpdatedAt: at, EventID: 1}}}
			started, release := make(chan struct{}), make(chan struct{})
			b := NewPGBackend(nil, nil, nil).WithTargetReadinessLoader(func(ctx context.Context, _ []Target) (map[string]TargetReadinessSnapshot, error) {
				close(started)
				select {
				case <-release:
					return map[string]TargetReadinessSnapshot{target.InstanceID: snapshot}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
			b.RecordTarget(target.AppID, target)
			done := make(chan error, 1)
			go func() {
				if path == "repair" {
					done <- b.ReconcileTargetReadiness(t.Context())
				} else {
					_, err := b.VerifyTargetReadiness(t.Context(), target)
					done <- err
				}
			}()
			<-started
			b.SetInstanceReadinessForTarget(target.AppID, target.InstanceID, target.WakeID, target.NodeID, "sidecar:proxy", "unready", at.Add(time.Second), 2)
			close(release)
			err := <-done
			if (path == "repair" && err != nil) || (path == "direct forwarding" && !errors.Is(err, ErrTargetReadinessUnavailable)) || b.Pick(target.AppID).OK || b.CapacityCount(target.AppID) != 1 {
				t.Fatalf("new source withdrawal masked: path=%s pick=%+v capacity=%d err=%v", path, b.Pick(target.AppID), b.CapacityCount(target.AppID), err)
			}
		})
	}
}

func TestTargetPublicationCertificateRequiresCompleteSourceSet(t *testing.T) {
	for _, mutation := range []string{"unset probe flag", "remove required source", "add source"} {
		t.Run(mutation, func(t *testing.T) {
			target, _ := repairReadyTarget()
			b := NewPGBackend(nil, nil, nil).WithTargetReadinessLoader(func(context.Context, []Target) (map[string]TargetReadinessSnapshot, error) {
				t.Fatal("bare publication tried to read configuration synchronously")
				return nil, nil
			})
			b.RecordTarget(target.AppID, target)
			copy := b.Pick(target.AppID).Target
			copy.ReadinessGates = cloneReadinessGates(copy.ReadinessGates)
			switch mutation {
			case "unset probe flag":
				copy.RequiresReadiness, copy.ReadinessGates = false, nil
			case "remove required source":
				copy.ReadinessGates.RequiredSources = copy.ReadinessGates.RequiredSources[:1]
			case "add source":
				copy.ReadinessGates.RequiredSources = append(copy.ReadinessGates.RequiredSources, "sidecar:other")
				copy.ReadinessGates.States["sidecar:other"] = ReadinessState{Ready: true, UpdatedAt: time.Now()}
			}
			if copy.routeReady() {
				t.Fatal("readiness certificate allowed a changed source configuration")
			}
		})
	}
}

func TestTargetPublicationCertificateCannotEnterForeignAppPicker(t *testing.T) {
	target := Target{AppID: "app", InstanceID: "instance", DeploymentID: "deployment", NodeID: "node", WakeID: "wake"}
	b := NewPGBackend(nil, nil, nil).WithTargetReadinessLoader(func(context.Context, []Target) (map[string]TargetReadinessSnapshot, error) {
		return map[string]TargetReadinessSnapshot{target.InstanceID: {AppID: target.AppID, InstanceID: target.InstanceID, DeploymentID: target.DeploymentID, NodeID: target.NodeID, WakeID: target.WakeID}}, nil
	})
	if err := b.RecordTargetWithReadiness(t.Context(), target.AppID, target); err != nil {
		t.Fatal(err)
	}
	b.RecordTarget("foreign", b.Pick(target.AppID).Target)
	if b.Pick("foreign").OK || b.CapacityCount("foreign") != 0 || !b.Pick(target.AppID).OK || b.CapacityCount(target.AppID) != 1 {
		t.Fatal("certificate-bearing target crossed its application cache owner")
	}
}
