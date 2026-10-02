// adr: 375
package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type readinessStoreFixture struct {
	configs                map[string]state.DeploymentReadinessConfig
	states                 map[string]map[string]state.InstanceReadiness
	configErr, stateErr    error
	deployments, instances []string
	reads                  int
}

func (s *readinessStoreFixture) DeploymentReadinessConfigs(_ context.Context, ids []string) (map[string]state.DeploymentReadinessConfig, error) {
	s.reads++
	s.deployments = append([]string(nil), ids...)
	return s.configs, s.configErr
}

func (s *readinessStoreFixture) LatestInstanceReadinessBySource(_ context.Context, ids []string) (map[string]map[string]state.InstanceReadiness, error) {
	s.reads++
	s.instances = append([]string(nil), ids...)
	return s.states, s.stateErr
}

func TestTargetReadinessLoaderIdentitySourcesAndBounds(t *testing.T) {
	at := time.Now()
	store := &readinessStoreFixture{configs: map[string]state.DeploymentReadinessConfig{
		"probe":     {AppID: "app", DeploymentID: "probe", OverrideReadinessProbe: []byte(`{"path":"/readyz"}`), Sidecars: []byte(`[{"name":"proxy","type":"sidecar","primary_ingress":true,"readiness_probe":{}}]`)},
		"none":      {AppID: "app", DeploymentID: "none"},
		"other":     {AppID: "other", DeploymentID: "other"},
		"malformed": {AppID: "app", DeploymentID: "malformed", OverrideReadinessProbe: []byte(`{`)},
	}, states: map[string]map[string]state.InstanceReadiness{
		"one": {"primary_app": {Ready: true, At: at, EventID: 1}, "sidecar:proxy": {Ready: false, At: at, EventID: 2}, "sidecar:irrelevant": {Ready: true, At: at, EventID: 3}},
	}}
	loader := newTargetReadinessLoader(store)
	inputs := []gateway.Target{
		{AppID: "app", DeploymentID: "probe", InstanceID: "one"},
		{AppID: "app", DeploymentID: "probe", InstanceID: "two"},
		{AppID: "app", DeploymentID: "none", InstanceID: "disabled"},
		{AppID: "app", DeploymentID: "other", InstanceID: "wrong-owner"},
		{AppID: "app", DeploymentID: "malformed", InstanceID: "invalid"},
		{AppID: "app", DeploymentID: "missing", InstanceID: "missing"},
	}
	got, err := loader(t.Context(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || store.reads != 2 || len(store.deployments) != 5 || !reflect.DeepEqual(store.instances, []string{"one", "two"}) {
		t.Fatalf("readiness read scope differs: snapshots=%+v store=%+v", got, store)
	}
	one := got["one"]
	if one.AppID != "app" || one.InstanceID != "one" || one.DeploymentID != "probe" || len(one.States) != 2 || !one.States["primary_app"].Ready || one.States["sidecar:proxy"].Ready ||
		!reflect.DeepEqual(one.RequiredSources, []string{"primary_app", "sidecar:proxy"}) || len(got["two"].States) != 0 || len(got["disabled"].RequiredSources) != 0 {
		t.Fatalf("readiness source projection differs: %+v", got)
	}
	for _, inputs := range [][]gateway.Target{nil, make([]gateway.Target, api.TrafficReadinessBatchSize+1)} {
		store.reads = 0
		_, err := loader(t.Context(), inputs)
		if (err != nil) != (len(inputs) > 0) || store.reads != 0 {
			t.Fatalf("unbounded or empty input reached store: reads=%d err=%v", store.reads, err)
		}
	}
}

func TestTargetReadinessLoaderStoreFailures(t *testing.T) {
	for _, source := range []string{"configuration", "observations"} {
		t.Run(source, func(t *testing.T) {
			failure := errors.New("read unavailable")
			store := &readinessStoreFixture{}
			if source == "configuration" {
				store.configErr = failure
			} else {
				store.stateErr = failure
			}
			got, err := newTargetReadinessLoader(store)(t.Context(), []gateway.Target{{AppID: "app", DeploymentID: "deployment", InstanceID: "instance"}})
			if !errors.Is(err, failure) || got != nil {
				t.Fatalf("store failure accepted: snapshots=%+v err=%v", got, err)
			}
		})
	}
}

func TestTargetReadinessRunWithDepsStopsRepairOnStartupFailure(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	backend := gateway.NewPGBackend(nil, nil, discardLogger()).WithTargetReadinessLoader(func(ctx context.Context, _ []gateway.Target) (map[string]gateway.TargetReadinessSnapshot, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	})
	backend.RecordTarget("app", gateway.Target{AppID: "app", InstanceID: "instance", DeploymentID: "deployment", NodeID: "node", RequiresReadiness: true})
	deps := defaultDeps()
	deps.backend = backend
	failure := errors.New("startup failed")
	deps.capCheck = func() error { <-started; return failure }
	if err := runWithDeps(t.Context(), discardLogger(), deps); !errors.Is(err, failure) {
		t.Fatalf("startup error=%v", err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("daemon returned before its readiness worker stopped")
	}
}
