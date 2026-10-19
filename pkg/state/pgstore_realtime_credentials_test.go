package state_test

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreRealtimeWorkerCredentialScopesOperations(t *testing.T) {
	s, ctx, ep := realtimeGroupFixture(t, "-rt-worker-cred", 2)
	group, err := s.CreateRealtimeConsumerGroup(ctx, ep, "orders", "billing", state.RealtimeConsumerGroupConfig{AfterSequence: "0"})
	if err != nil {
		t.Fatalf("CreateRealtimeConsumerGroup: %v", err)
	}
	issued, err := s.IssueRealtimeWorkerCredential(ctx, ep, "orders", "billing", state.RealtimeWorkerCredentialRequest{Name: "billing-worker", Permissions: []string{"claim", "complete"}})
	if err != nil {
		t.Fatalf("IssueRealtimeWorkerCredential: %v", err)
	}
	if issued.Credential == "" || issued.Info.GroupID != group.GroupID {
		t.Fatalf("issued = %+v, want a credential bound to group %s", issued.Info, group.GroupID)
	}
	grant, err := s.AuthenticateRealtimeWorkerCredential(ctx, group.GroupID, issued.Credential)
	if err != nil {
		t.Fatalf("AuthenticateRealtimeWorkerCredential: %v", err)
	}
	if grant.EndpointID != ep || grant.Channel != "orders" || grant.Group != "billing" {
		t.Fatalf("grant = %+v", grant)
	}
	if _, err := s.AuthenticateRealtimeWorkerCredential(ctx, uuid.NewString(), issued.Credential); !errors.Is(err, state.ErrRealtimeWorkerCredentialDenied) {
		t.Fatalf("credential on another group err = %v, want denied", err)
	}

	claimCtx := state.WithRealtimeWorkerGrant(ctx, grant, "claim")
	claim, err := s.ClaimRealtimeConsumerGroup(claimCtx, ep, "orders", "billing", "worker-a")
	if err != nil || claim == nil {
		t.Fatalf("scoped claim = %+v, %v", claim, err)
	}
	// A granted operation succeeds; an ungranted one is denied.
	completeCtx := state.WithRealtimeWorkerGrant(ctx, grant, "complete")
	if _, err := s.SettleRealtimeConsumerGroup(completeCtx, ep, "orders", "billing", state.RealtimeConsumerGroupSettlement{Sequence: claim.Message.Sequence, ClaimToken: claim.ClaimToken, Action: "complete"}); err != nil {
		t.Fatalf("scoped complete: %v", err)
	}
	pauseCtx := state.WithRealtimeWorkerGrant(ctx, grant, "pause")
	if _, err := s.SetRealtimeConsumerGroupPaused(pauseCtx, ep, "orders", "billing", true); !errors.Is(err, state.ErrRealtimeWorkerCredentialDenied) {
		t.Fatalf("ungranted pause err = %v, want denied", err)
	}
	// The context operation must match the store call.
	if _, err := s.ClaimRealtimeConsumerGroup(completeCtx, ep, "orders", "billing", "worker-a"); !errors.Is(err, state.ErrRealtimeWorkerCredentialDenied) {
		t.Fatalf("claim under complete context err = %v, want denied", err)
	}

	rotated, err := s.RotateRealtimeWorkerCredential(ctx, ep, "orders", "billing", issued.Info.ID, 0)
	if err != nil {
		t.Fatalf("RotateRealtimeWorkerCredential: %v", err)
	}
	if _, err := s.AuthenticateRealtimeWorkerCredential(ctx, group.GroupID, issued.Credential); !errors.Is(err, state.ErrRealtimeWorkerCredentialDenied) {
		t.Fatalf("pre-rotation credential err = %v, want denied", err)
	}
	// A grant authenticated before rotation is fenced inside the transaction.
	if _, err := s.ClaimRealtimeConsumerGroup(claimCtx, ep, "orders", "billing", "worker-a"); !errors.Is(err, state.ErrRealtimeWorkerCredentialDenied) {
		t.Fatalf("stale grant claim err = %v, want denied", err)
	}
	if _, err := s.AuthenticateRealtimeWorkerCredential(ctx, group.GroupID, rotated.Credential); err != nil {
		t.Fatalf("rotated credential: %v", err)
	}
	if err := s.RevokeRealtimeWorkerCredential(ctx, ep, "orders", "billing", issued.Info.ID); err != nil {
		t.Fatalf("RevokeRealtimeWorkerCredential: %v", err)
	}
	if _, err := s.AuthenticateRealtimeWorkerCredential(ctx, group.GroupID, rotated.Credential); !errors.Is(err, state.ErrRealtimeWorkerCredentialDenied) {
		t.Fatalf("revoked credential err = %v, want denied", err)
	}
	creds, err := s.ListRealtimeWorkerCredentials(ctx, ep, "orders", "billing")
	if err != nil || len(creds) != 1 || creds[0].RevokedAt == nil || creds[0].Generation != "2" {
		t.Fatalf("listed credentials = %+v, %v", creds, err)
	}
}

func TestPgStoreRealtimePublisherCredentialPublishes(t *testing.T) {
	s, ctx, ep := realtimeGroupFixture(t, "-rt-publisher-cred", 0)
	issued, err := s.IssueRealtimePublisherCredential(ctx, ep, state.RealtimePublisherCredentialRequest{
		Name: "orders-service", Channels: []string{"orders"}, EventTypes: []string{"order.created"}, PublishesPerMinute: 2,
	})
	if err != nil {
		t.Fatalf("IssueRealtimePublisherCredential: %v", err)
	}
	req := state.RealtimePublisherMessageRequest{EventType: "order.created", DataBase64: base64.StdEncoding.EncodeToString([]byte(`{"id":1}`))}
	key := uuid.NewString()
	first, err := s.PublishRealtimeWithCredential(ctx, ep, "orders", issued.Credential, req, key)
	if err != nil || first.Replayed || first.Message.Sequence != 1 {
		t.Fatalf("publish = %+v, %v", first, err)
	}
	if first.Message.Metadata["event_type"] != "order.created" {
		t.Fatalf("metadata = %+v, want event_type stamped", first.Message.Metadata)
	}
	replay, err := s.PublishRealtimeWithCredential(ctx, ep, "orders", issued.Credential, req, key)
	if err != nil || !replay.Replayed || replay.Message.Sequence != 1 {
		t.Fatalf("idempotent replay = %+v, %v", replay, err)
	}
	changed := req
	changed.DataBase64 = base64.StdEncoding.EncodeToString([]byte(`{"id":2}`))
	if _, err := s.PublishRealtimeWithCredential(ctx, ep, "orders", issued.Credential, changed, key); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed content under same key err = %v, want ErrConflict", err)
	}
	if _, err := s.PublishRealtimeWithCredential(ctx, ep, "payments", issued.Credential, req, uuid.NewString()); !errors.Is(err, state.ErrRealtimePublisherPermission) {
		t.Fatalf("ungranted channel err = %v, want permission error", err)
	}
	other := req
	other.EventType = "order.deleted"
	if _, err := s.PublishRealtimeWithCredential(ctx, ep, "orders", issued.Credential, other, uuid.NewString()); !errors.Is(err, state.ErrRealtimePublisherPermission) {
		t.Fatalf("ungranted event type err = %v, want permission error", err)
	}
	if _, err := s.PublishRealtimeWithCredential(ctx, ep, "orders", issued.Credential, changed, uuid.NewString()); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if _, err := s.PublishRealtimeWithCredential(ctx, ep, "orders", issued.Credential, changed, uuid.NewString()); !errors.Is(err, state.ErrRealtimePublisherRate) {
		t.Fatalf("third publish in a minute err = %v, want rate limit", err)
	}
	if err := s.RevokeRealtimePublisherCredential(ctx, ep, issued.Info.ID); err != nil {
		t.Fatalf("RevokeRealtimePublisherCredential: %v", err)
	}
	if _, err := s.PublishRealtimeWithCredential(ctx, ep, "orders", issued.Credential, req, uuid.NewString()); !errors.Is(err, state.ErrRealtimePublisherCredentialDenied) {
		t.Fatalf("revoked publish err = %v, want denied", err)
	}
}

// Ticket endpoints need not use OIDC, so access controls must accept any
// endpoint the handler admits (ADR-1117).
func TestRealtimeSubscriptionTicketHonorsAccessOnNonOIDCEndpoint(t *testing.T) {
	for _, name := range []string{"memory", "postgres"} {
		t.Run(name, func(t *testing.T) {
			var store state.Store
			if name == "memory" {
				store = state.NewMemStore()
			} else {
				pg, _ := pgStore(t)
				store = pg
			}
			tickets := store.(state.RealtimeSubscriptionTicketStore)
			access := store.(state.ManagedRealtimeAccessStore)
			ctx := context.Background()
			owner, err := store.CreateAccount(ctx, "realtime-ticket-"+uuid.NewString()+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: owner.ID, Slug: "ticket-" + uuid.NewString()[:8]})
			if err != nil {
				t.Fatal(err)
			}
			endpoint, err := store.(state.ManagedRealtimeEndpointStore).CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(owner.ID, app.ID), 10, 50)
			if err != nil {
				t.Fatal(err)
			}
			if endpoint.AuthMode == "oidc_jwt" {
				t.Fatalf("fixture endpoint uses %q, want a non-OIDC mode", endpoint.AuthMode)
			}
			ep := endpoint.ID
			token, ticket, err := tickets.IssueRealtimeSubscriptionTicket(ctx, ep, "user-1", []string{"orders", "alerts"}, 60)
			if err != nil {
				t.Fatalf("IssueRealtimeSubscriptionTicket: %v", err)
			}
			if ticket.Principal != "user-1" || !ticket.ExpiresAt.After(ticket.IssuedAt) {
				t.Fatalf("ticket = %+v", ticket)
			}
			read, err := tickets.ReadRealtimeSubscriptionTicket(ctx, ep, token)
			if err != nil || !slices.Equal(read.Channels, []string{"orders", "alerts"}) {
				t.Fatalf("read = %+v, %v", read, err)
			}
			if _, err := tickets.ReadRealtimeSubscriptionTicket(ctx, uuid.NewString(), token); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("ticket on another endpoint err = %v, want ErrNotFound", err)
			}
			if _, err := access.ChangeManagedRealtimeAccess(ctx, ep, "user-1", "revoke_channel", "alerts"); err != nil {
				t.Fatalf("revoke channel: %v", err)
			}
			read, err = tickets.ReadRealtimeSubscriptionTicket(ctx, ep, token)
			if err != nil || !slices.Equal(read.Channels, []string{"orders"}) {
				t.Fatalf("after channel revoke = %+v, %v, want only orders", read, err)
			}
			if _, err := access.ChangeManagedRealtimeAccess(ctx, ep, "user-1", "revoke_sessions", ""); err != nil {
				t.Fatalf("revoke sessions: %v", err)
			}
			if _, err := tickets.ReadRealtimeSubscriptionTicket(ctx, ep, token); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("after session revoke err = %v, want ErrNotFound", err)
			}
			if _, _, err := tickets.IssueRealtimeSubscriptionTicket(ctx, ep, "user-1", []string{"orders"}, 301); err == nil {
				t.Fatal("ticket TTL above 300 s was accepted")
			}
		})
	}
}

func TestRealtimeSubscriptionTicketRevocation(t *testing.T) {
	for _, name := range []string{"memory", "postgres"} {
		t.Run(name, func(t *testing.T) {
			var store state.Store
			if name == "memory" {
				store = state.NewMemStore()
			} else {
				pg, _ := pgStore(t)
				store = pg
			}
			tickets := store.(state.RealtimeSubscriptionTicketStore)
			revoker := store.(state.RealtimeSubscriptionTicketRevoker)
			ctx := context.Background()
			owner, err := store.CreateAccount(ctx, "realtime-revoke-"+uuid.NewString()+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: owner.ID, Slug: "revoke-" + uuid.NewString()[:8]})
			if err != nil {
				t.Fatal(err)
			}
			endpoint, err := store.(state.ManagedRealtimeEndpointStore).CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(owner.ID, app.ID), 10, 50)
			if err != nil {
				t.Fatal(err)
			}
			ep := endpoint.ID
			issue := func(principal string) (string, state.RealtimeSubscriptionTicket) {
				t.Helper()
				token, ticket, err := tickets.IssueRealtimeSubscriptionTicket(ctx, ep, principal, []string{"orders"}, 60)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := uuid.Parse(ticket.ID); err != nil {
					t.Fatalf("ticket id = %q, want a UUID", ticket.ID)
				}
				return token, ticket
			}
			first, firstTicket := issue("user-1")
			second, _ := issue("user-1")
			other, _ := issue("user-2")
			if read, err := tickets.ReadRealtimeSubscriptionTicket(ctx, ep, first); err != nil || read.ID != firstTicket.ID {
				t.Fatalf("read = %+v, %v, want the issued ID", read, err)
			}

			if err := revoker.RevokeRealtimeSubscriptionTicket(ctx, ep, firstTicket.ID); err != nil {
				t.Fatalf("revoke by id: %v", err)
			}
			if _, err := tickets.ReadRealtimeSubscriptionTicket(ctx, ep, first); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("revoked ticket read err = %v, want ErrNotFound", err)
			}
			if err := revoker.RevokeRealtimeSubscriptionTicket(ctx, ep, firstTicket.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("second revoke err = %v, want ErrNotFound", err)
			}
			if err := revoker.RevokeRealtimeSubscriptionTicket(ctx, uuid.NewString(), firstTicket.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("revoke on another endpoint err = %v, want ErrNotFound", err)
			}
			if err := revoker.RevokeRealtimeSubscriptionTicket(ctx, ep, "not-a-uuid"); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("malformed id err = %v, want ErrInvalidArgument", err)
			}

			// Principal-wide revocation ends user-1's remaining ticket only.
			if n, err := revoker.RevokeRealtimeSubscriptionTicketsForPrincipal(ctx, ep, "user-1"); err != nil || n != 1 {
				t.Fatalf("revoke user-1 = %d, %v, want 1", n, err)
			}
			if _, err := tickets.ReadRealtimeSubscriptionTicket(ctx, ep, second); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("user-1 ticket after principal revoke err = %v", err)
			}
			if _, err := tickets.ReadRealtimeSubscriptionTicket(ctx, ep, other); err != nil {
				t.Fatalf("user-2 ticket was revoked too: %v", err)
			}
			if n, err := revoker.RevokeRealtimeSubscriptionTicketsForPrincipal(ctx, ep, "user-1"); err != nil || n != 0 {
				t.Fatalf("repeat revoke = %d, %v, want 0", n, err)
			}
			if _, err := revoker.RevokeRealtimeSubscriptionTicketsForPrincipal(ctx, ep, ""); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("empty principal err = %v", err)
			}
		})
	}
}
