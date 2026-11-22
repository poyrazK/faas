package main

import (
	"context"
	"testing"
)

func TestResourceOwnershipIsSharedByReadAndSubscription(t *testing.T) {
	auth := testScopedAuthenticator(t, `{"mode":"external-oauth","issuer":"https://issuer.example","jwks_url":"https://issuer.example/jwks","resource":"https://mcp.example/mcp","scopes":["mcp:catalog"]}`)
	caller := scopedContext("mcp:catalog")
	caller.subject = "demo-caller-a"
	for _, subscription := range []bool{false, true} {
		if !resourceAccessAllowed(t.Context(), "customer://records/example-1", auth, caller, subscription) {
			t.Fatal("owner lost access")
		}
		if resourceAccessAllowed(t.Context(), "customer://records/example-2", auth, caller, subscription) {
			t.Fatal("another caller's activity is visible")
		}
	}
}

func TestApplicationResourceSubscriptionRequiresExplicitAuthorizer(t *testing.T) {
	definition := map[string]any{"name": "guarded_regression", "uri": "fixture://guarded-regression"}
	handler := MCPResourceHandler(func(context.Context, string, map[string]string, *MCPRequestContext) ([]map[string]any, error) {
		t.Fatal("authorization executed a read handler")
		return nil, nil
	})
	if err := RegisterMCPResource(definition, handler); err != nil {
		t.Fatal(err)
	}
	defer RemoveMCPResource("guarded_regression")
	auth := testScopedAuthenticator(t, `{"mode":"open"}`)
	if resourceAccessAllowed(t.Context(), "fixture://guarded-regression", auth, nil, true) {
		t.Fatal("missing subscription authorizer accepted")
	}
	allowed := true
	if err := ReplaceMCPResource("guarded_regression", definition, handler, func(context.Context, string, *MCPRequestContext) bool { return allowed }); err != nil {
		t.Fatal(err)
	}
	if !resourceAccessAllowed(t.Context(), "fixture://guarded-regression", auth, nil, true) {
		t.Fatal("explicit authorization denied")
	}
	allowed = false
	for _, subscription := range []bool{false, true} {
		if resourceAccessAllowed(t.Context(), "fixture://guarded-regression", auth, nil, subscription) {
			t.Fatal("revocation ignored")
		}
	}
}
