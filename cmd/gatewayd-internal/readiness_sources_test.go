package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestReadinessSourcesForDeployment(t *testing.T) {
	deployment := state.Deployment{
		OverrideReadinessProbe: json.RawMessage(`{"path":"/readyz"}`),
		Sidecars: json.RawMessage(`[
			{"name":"proxy","type":"sidecar","primary_ingress":true,"readiness_probe":{}},
			{"name":"metrics","type":"sidecar","readiness_probe":{}},
			{"name":"setup","type":"init","primary_ingress":true,"readiness_probe":{}}
		]`),
	}
	got, err := readinessSourcesForDeployment(deployment)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"primary_app", "sidecar:proxy"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readiness sources = %v, want %v", got, want)
	}
}

func TestReadinessSourcesForDeploymentRejectsMalformedConfig(t *testing.T) {
	for _, deployment := range []state.Deployment{
		{OverrideReadinessProbe: json.RawMessage(`{`)},
		{Sidecars: json.RawMessage(`{`)},
	} {
		if _, err := readinessSourcesForDeployment(deployment); err == nil {
			t.Fatalf("readinessSourcesForDeployment(%+v) unexpectedly succeeded", deployment)
		}
	}
}
