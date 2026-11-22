package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

const scopedAuthConfig = `{
	"mode":"external-oauth",
	"issuer":"https://issuer.example",
	"jwks_url":"https://issuer.example/jwks",
	"resource":"https://mcp.example/mcp",
	"scopes":["mcp:catalog"],
	"resource_scopes":{
		"greeting://welcome":[],
		"customer://records/{recordId}":["records:read"]
	},
	"prompt_scopes":{"summarize":["reports:read"]}
}`

func testScopedAuthenticator(t *testing.T, authJSON string) *authenticator {
	t.Helper()
	auth, err := newAuthenticator(mcpConfig{
		Version:   1,
		Endpoint:  "/mcp",
		Transport: "streamable-http",
		Mode:      "stateless",
		Auth:      json.RawMessage(authJSON),
	})
	if err != nil {
		t.Fatalf("newAuthenticator() error = %v", err)
	}
	return auth
}

func scopedContext(scopes ...string) *authContext {
	granted := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		granted[scope] = struct{}{}
	}
	return &authContext{scopes: granted}
}

func TestResourceAndPromptScopesFilterCatalogs(t *testing.T) {
	auth := testScopedAuthenticator(t, scopedAuthConfig)
	reader := scopedContext("mcp:catalog")
	if got := len(auth.visibleResources(reader)); got != 1 {
		t.Fatalf("visible resource count = %d, want 1", got)
	}
	if got := len(auth.visibleResourceTemplates(reader)); got != 0 {
		t.Fatalf("visible resource-template count = %d, want 0", got)
	}
	if got := len(auth.visiblePrompts(reader)); got != 0 {
		t.Fatalf("visible prompt count = %d, want 0", got)
	}

	writer := scopedContext("mcp:catalog", "records:read", "reports:read")
	if got := len(auth.visibleResourceTemplates(writer)); got != 1 {
		t.Fatalf("writer resource-template count = %d, want 1", got)
	}
	if got := len(auth.visiblePrompts(writer)); got != 1 {
		t.Fatalf("writer prompt count = %d, want 1", got)
	}
}

func TestResourceAndPromptScopeFailuresAdvertiseRequiredScopes(t *testing.T) {
	auth := testScopedAuthenticator(t, scopedAuthConfig)
	reader := scopedContext("mcp:catalog")
	for _, test := range []struct {
		name    string
		failure *authFailure
		want    string
	}{
		{
			name:    "resource template",
			failure: auth.resourceAllowed("customer://records/example-1", reader),
			want:    `scope="mcp:catalog records:read"`,
		},
		{
			name:    "prompt",
			failure: auth.promptAllowed("summarize", reader),
			want:    `scope="mcp:catalog reports:read"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.failure == nil || test.failure.error != "insufficient_scope" {
				t.Fatalf("failure = %#v, want insufficient_scope", test.failure)
			}
			response := httptest.NewRecorder()
			auth.writeFailure(response, test.failure)
			if response.Code != 403 {
				t.Fatalf("status = %d, want 403", response.Code)
			}
			challenge := response.Header().Get("WWW-Authenticate")
			if !strings.Contains(challenge, test.want) || !strings.Contains(challenge, `error="insufficient_scope"`) {
				t.Fatalf("challenge = %q, want required scopes and insufficient_scope", challenge)
			}
		})
	}
}

func TestCompletionReferencesUsePromptAndResourceScopes(t *testing.T) {
	auth := testScopedAuthenticator(t, scopedAuthConfig)
	reader := scopedContext("mcp:catalog")
	for _, test := range []struct {
		name          string
		referenceType string
		identifier    string
		want          string
	}{
		{name: "prompt", referenceType: "ref/prompt", identifier: "summarize", want: `scope="mcp:catalog reports:read"`},
		{name: "resource template", referenceType: "ref/resource", identifier: "customer://records/{recordId}", want: `scope="mcp:catalog records:read"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			failure := auth.completionReferenceAllowed(test.referenceType, test.identifier, reader)
			if failure == nil || failure.error != "insufficient_scope" {
				t.Fatalf("completion failure = %#v, want insufficient_scope", failure)
			}
			response := httptest.NewRecorder()
			auth.writeFailure(response, failure)
			if !strings.Contains(response.Header().Get("WWW-Authenticate"), test.want) {
				t.Fatalf("challenge = %q, want %q", response.Header().Get("WWW-Authenticate"), test.want)
			}
		})
	}
	writer := scopedContext("mcp:catalog", "records:read", "reports:read")
	for _, test := range []struct {
		referenceType string
		identifier    string
		want          string
	}{
		{referenceType: "ref/prompt", identifier: "private_prompt", want: "prompt_access_denied"},
		{referenceType: "ref/resource", identifier: "task://tasks/{taskId}", want: "resource_access_denied"},
	} {
		failure := auth.completionReferenceAllowed(test.referenceType, test.identifier, writer)
		if failure == nil || failure.error != test.want || len(failure.scopes) != 0 {
			t.Fatalf("completion failure = %#v, want unchallenged %s", failure, test.want)
		}
	}
}

func TestUnlistedResourceAndPromptAreDeniedWithoutScopeChallenge(t *testing.T) {
	auth := testScopedAuthenticator(t, scopedAuthConfig)
	reader := scopedContext("mcp:catalog", "records:read", "reports:read")
	for _, test := range []struct {
		name    string
		failure *authFailure
		want    string
	}{
		{name: "resource", failure: auth.resourceAllowed("task://tasks/task-1", reader), want: "resource_access_denied"},
		{name: "prompt", failure: auth.promptAllowed("private_prompt", reader), want: "prompt_access_denied"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.failure == nil || test.failure.error != test.want {
				t.Fatalf("failure = %#v, want %q", test.failure, test.want)
			}
			response := httptest.NewRecorder()
			auth.writeFailure(response, test.failure)
			if response.Code != 403 {
				t.Fatalf("status = %d, want 403", response.Code)
			}
			if challenge := response.Header().Get("WWW-Authenticate"); challenge != "" {
				t.Fatalf("unexpected challenge for unlisted entry: %q", challenge)
			}
		})
	}
}

func TestExplicitEmptyResourceAndPromptPoliciesDenyAllEntries(t *testing.T) {
	auth := testScopedAuthenticator(t, `{
		"mode":"external-oauth",
		"issuer":"https://issuer.example",
		"jwks_url":"https://issuer.example/jwks",
		"resource":"https://mcp.example/mcp",
		"scopes":["mcp:catalog"],
		"resource_scopes":{},
		"prompt_scopes":{}
	}`)
	reader := scopedContext("mcp:catalog")
	if got := len(auth.visibleResources(reader)) + len(auth.visibleResourceTemplates(reader)) + len(auth.visiblePrompts(reader)); got != 0 {
		t.Fatalf("visible resource and prompt count = %d, want 0", got)
	}
	if failure := auth.resourceAllowed("greeting://welcome", reader); failure == nil || failure.error != "resource_access_denied" {
		t.Fatalf("resource failure = %#v, want resource_access_denied", failure)
	}
	if failure := auth.promptAllowed("summarize", reader); failure == nil || failure.error != "prompt_access_denied" {
		t.Fatalf("prompt failure = %#v, want prompt_access_denied", failure)
	}
}
