package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPOAuthRetirementPublicPrivacy(t *testing.T) {
	r := MCPOAuthRetirement{Revocation: &MCPCatalogOAuthRevocation{ID: "record", State: "pending", Ciphertext: []byte("PRIVATE_GRANT"), ClientCiphertext: []byte("PRIVATE_CLIENT"), Config: &MCPCatalogObserverOAuthConfig{ClientID: "PRIVATE_BINDING"}, LeaseID: "PRIVATE_LEASE"}, History: []MCPOAuthRetirementEvent{{Action: "queued"}}}
	public := MCPOAuthRetirementPublic(r)
	data, _ := json.Marshal(public)
	if strings.Contains(string(data), "private_") || strings.Contains(string(data), "PRIVATE") {
		t.Fatal("public retirement exposed private data")
	}
	if r.Revocation.Config == nil || r.Revocation.Ciphertext == nil {
		t.Fatal("projection mutated stored grant")
	}
}
