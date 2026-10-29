package apidserver_test

import (
	"context"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gitlabsource"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestAccountIntegrations_SealAndOpenInTheDomain seals GitLab OAuth tokens
// to the account domain alone and opens them, and a Slack bot token sealed
// to the domain alone through apid's Slack delivery reader (ADR
// 20261022111812).
func TestAccountIntegrations_SealAndOpenInTheDomain(t *testing.T) {
	fleet, domain := mustSealIdentity(t), mustSealIdentity(t)
	withAccountDomain(t, fleet, true, domain, sealdomain.WriteDomain)

	tokens := gitlabsource.Tokens{Access: "glpat-access", Refresh: "glpat-refresh", Expires: time.Now().Add(time.Hour).UTC().Truncate(time.Second)}
	sealed, err := apidserver.SealGitLabTokens(tokens)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := secretbox.OpenBytes(fleet, sealed); err == nil {
		t.Fatal("the fleet identity opens GitLab tokens sealed in domain mode")
	}
	got, err := apidserver.OpenGitLabTokens(context.Background(), sealed)
	if err != nil || got.Access != tokens.Access || got.Refresh != tokens.Refresh {
		t.Fatalf("GitLab tokens open = %+v, %v", got, err)
	}

	token, err := secretbox.SealBytesTo([]*age.X25519Recipient{domain.Recipient()}, state.IssueSlackTokenNamespace, []byte("xoxb-domain-token"), api.IssueSlackTokenMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if opened, err := apidserver.OpenIssueSlackToken(token); err != nil || opened != "xoxb-domain-token" {
		t.Fatalf("Slack token open = %q, %v", opened, err)
	}
}
