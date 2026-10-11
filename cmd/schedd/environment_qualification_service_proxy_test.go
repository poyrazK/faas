package main

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentQualificationServiceProxyResolverIsNodeAndTransportBound(t *testing.T) {
	store := qualificationDispatchNodeStoreFake{node: state.ComputeNode{ID: "local-node", Name: state.DefaultLocalNodeName}}
	resolve := environmentQualificationServiceProxyResolver(store, "", " http://10.100.0.1:10081 ", "https://gateway.internal:443")
	if got, err := resolve(t.Context(), "local-node", api.ServiceBindingTransportHTTP); err != nil || got != "http://10.100.0.1:10081" {
		t.Fatalf("HTTP service proxy = %q, %v", got, err)
	}
	if got, err := resolve(t.Context(), "local-node", api.ServiceBindingTransportHTTPS); err != nil || got != "https://gateway.internal:443" {
		t.Fatalf("HTTPS service proxy = %q, %v", got, err)
	}
	if _, err := resolve(t.Context(), "remote-node", api.ServiceBindingTransportHTTP); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("remote node service proxy was accepted: %v", err)
	}
}

func TestEnvironmentQualificationServiceProxyResolverRequiresTransportURL(t *testing.T) {
	store := qualificationDispatchNodeStoreFake{node: state.ComputeNode{ID: "local-node", Name: state.DefaultLocalNodeName}}
	resolve := environmentQualificationServiceProxyResolver(store, "", "http://10.100.0.1:10081", "")
	if _, err := resolve(t.Context(), "local-node", api.ServiceBindingTransportHTTPS); !errors.Is(err, state.ErrEnvironmentWorkloadPreparationUnavailable) {
		t.Fatalf("missing HTTPS service proxy was accepted: %v", err)
	}
	if _, err := environmentQualificationServiceProxyResolver(store, "", "", "")(t.Context(), "local-node", api.ServiceBindingTransportHTTP); !errors.Is(err, state.ErrEnvironmentWorkloadPreparationUnavailable) {
		t.Fatalf("missing HTTP service proxy was accepted: %v", err)
	}
}
