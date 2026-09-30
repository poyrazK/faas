package state_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardResourceTestStore interface {
	standardTestStore
	state.ApplicationStandardResourceStore
}

func TestMemApplicationStandardResources(t *testing.T) {
	standardResourceLifecycle(t, state.NewMemStore())
}

func standardResourceLifecycle(t *testing.T, store standardResourceTestStore) {
	t.Helper()
	ctx := context.Background()
	actor, err := store.CreateAccount(ctx, "standard-resources@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "standard-resource-org", Name: "Resources", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateOrg(ctx, state.Org{Slug: "standard-resource-other", Name: "Other", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	input := state.ApplicationStandardLogDestinationCreate{OrgID: org.ID, ActorID: actor.ID, Name: "Central logs", Kind: "http_json", TargetURL: "https://logs.example.com/ingest", AuthHeaderSealed: []byte("opaque sealed credential")}
	destination, err := store.CreateApplicationStandardLogDestination(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	input.AuthHeaderSealed[0] = 'X'
	destination.AuthHeaderSealed[0] = 'Y'
	read, err := store.GetApplicationStandardLogDestination(ctx, org.ID, destination.ID)
	if err != nil || string(read.AuthHeaderSealed) != "opaque sealed credential" || !read.HasAuthHeader || len(read.ConfigHash) != 64 {
		t.Fatalf("destination alias or hash: %+v %v", read, err)
	}
	wire, err := json.Marshal(read)
	if err != nil || strings.Contains(string(wire), "sealed credential") || strings.Contains(string(wire), "AuthHeaderSealed") {
		t.Fatalf("credential exposed in JSON: %s %v", wire, err)
	}
	if _, err := store.GetApplicationStandardLogDestination(ctx, other.ID, destination.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-org destination: %v", err)
	}
	input.AuthHeaderSealed = nil
	second, err := store.CreateApplicationStandardLogDestination(ctx, input)
	if err != nil || second.HasAuthHeader || second.ConfigHash == read.ConfigHash || second.ID == read.ID {
		t.Fatalf("credential replacement: %+v %v", second, err)
	}
	page, err := store.ListApplicationStandardLogDestinations(ctx, org.ID, "", 1)
	if err != nil || len(page) != 1 {
		t.Fatalf("first page: %+v %v", page, err)
	}
	next, err := store.ListApplicationStandardLogDestinations(ctx, org.ID, page[0].ID, 1)
	if err != nil || len(next) != 1 || next[0].ID == page[0].ID {
		t.Fatalf("next page: %+v %v", next, err)
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := store.CreateApplicationStandardPublisher(ctx, state.ApplicationStandardPublisherCreate{OrgID: org.ID, ActorID: actor.ID, Name: "Production CI", PublicKeyDER: der})
	if err != nil || len(publisher.Fingerprint) != 64 || publisher.PublicKeyDER == "" {
		t.Fatalf("publisher: %+v %v", publisher, err)
	}
	if _, err := store.GetApplicationStandardPublisher(ctx, other.ID, publisher.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-org publisher: %v", err)
	}
	pubs, err := store.ListApplicationStandardPublishers(ctx, org.ID, "", 10)
	if err != nil || len(pubs) != 1 || pubs[0].Fingerprint != publisher.Fingerprint {
		t.Fatalf("publisher list: %+v %v", pubs, err)
	}
	definition := json.RawMessage(fmt.Sprintf(`{"log_destinations":{"mode":"mandatory","value":[%q]},"trusted_publishers":{"mode":"restricted","value":[%q]}}`, destination.ID, publisher.ID))
	request := state.ApplicationStandardPublish{OrgID: org.ID, ActorID: actor.ID, Slug: "resource-baseline", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: definition}}
	if _, err := store.PublishApplicationStandardVersion(ctx, request); err != nil {
		t.Fatalf("valid references: %v", err)
	}
	request.OrgID = other.ID
	if _, err := store.PublishApplicationStandardVersion(ctx, request); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("foreign references accepted: %v", err)
	}
	request.OrgID = org.ID
	request.Slug = "missing-resource"
	request.Definition = json.RawMessage(fmt.Sprintf(`{"log_destinations":{"mode":"mandatory","value":[%q]}}`, uuid.NewString()))
	if _, err := store.PublishApplicationStandardVersion(ctx, request); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("missing reference accepted: %v", err)
	}
	for _, target := range []string{"http://logs.example.com/ingest", "https://user:secret@logs.example.com/", "https://logs.example.com/?token=secret", "https://logs.example.com/#secret"} {
		input.TargetURL = target
		if _, err := store.CreateApplicationStandardLogDestination(ctx, input); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("unsafe destination accepted %s: %v", target, err)
		}
	}
	wrongKey, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	wrongDER, _ := x509.MarshalPKIXPublicKey(&wrongKey.PublicKey)
	for _, key := range [][]byte{nil, []byte("not a key"), wrongDER} {
		if _, err := store.CreateApplicationStandardPublisher(ctx, state.ApplicationStandardPublisherCreate{OrgID: org.ID, ActorID: actor.ID, Name: "Bad", PublicKeyDER: key}); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("unsupported key accepted: %v", err)
		}
	}
}
