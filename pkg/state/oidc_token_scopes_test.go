package state

import (
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestOIDCExchangedTokenToAPIKeyPreservesScopeProfile(t *testing.T) {
	want := []string{"project_environments:read", "project_environments:qualify"}
	token := OIDCExchangedToken{Scopes: append([]string(nil), want...)}
	key := token.ToAPIKey()
	if !reflect.DeepEqual(key.Scopes, want) {
		t.Fatalf("API key scopes = %v, want %v", key.Scopes, want)
	}
	key.Scopes[0] = "changed"
	if token.Scopes[0] != want[0] {
		t.Fatal("API key scope mutation changed the persisted OIDC token")
	}
}

func TestOIDCExchangedTokenLegacyRowsRemainDeployOnly(t *testing.T) {
	key := (OIDCExchangedToken{}).ToAPIKey()
	want := []string{api.ScopeDeployWrite}
	if !reflect.DeepEqual(key.Scopes, want) {
		t.Fatalf("legacy API key scopes = %v, want %v", key.Scopes, want)
	}
}

func TestMemStoreOIDCExchangedTokenDefaultsScopesToDeployOnly(t *testing.T) {
	store := NewMemStore()
	token := &OIDCExchangedToken{TokenHash: []byte("legacy"), ExpiresAt: time.Now().Add(time.Minute)}
	if _, err := store.InsertOIDCExchangedToken(t.Context(), token); err != nil {
		t.Fatalf("insert OIDC token: %v", err)
	}
	stored, err := store.GetOIDCExchangedTokenByHash(t.Context(), token.TokenHash)
	if err != nil {
		t.Fatalf("read OIDC token: %v", err)
	}
	want := []string{api.ScopeDeployWrite}
	if !reflect.DeepEqual(stored.Scopes, want) {
		t.Fatalf("stored OIDC scopes = %v, want %v", stored.Scopes, want)
	}
}

func TestOIDCExchangedTokenRowMappingPreservesScopeProfile(t *testing.T) {
	want := []string{"project_environments:read", "project_environments:qualify"}
	token := oidcExchangedTokenFromRow(sqlc.GetOIDCExchangedTokenByHashRow{Scopes: want})
	if !reflect.DeepEqual(token.Scopes, want) {
		t.Fatalf("mapped OIDC token scopes = %v, want %v", token.Scopes, want)
	}
}
