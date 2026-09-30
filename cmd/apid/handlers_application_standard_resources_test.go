package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func TestApplicationStandardResourceAPI(t *testing.T) {
	identity, restore := withTestIdentities(t)
	defer restore()
	e := setup(t, api.PlanPro)
	org := seedSharedOrgWithOwner(t, e, "resource-api-org", "Resources", api.PlanPro)
	base := "/v1/orgs/" + org.Slug + "/application-standard-"
	secret := "Authorization: Bearer standard-destination-test-secret"
	response := e.do(t, http.MethodPost, base+"log-destinations", api.CreateApplicationStandardLogDestinationRequest{Name: "Central logs", Kind: "http_json", TargetURL: "https://8.8.8.8/ingest", AuthHeader: secret}, nil)
	if response.Code != http.StatusCreated || strings.Contains(response.Body.String(), "standard-destination-test-secret") {
		t.Fatalf("destination response: %d %s", response.Code, response.Body)
	}
	var destination api.ApplicationStandardLogDestination
	if err := json.Unmarshal(response.Body.Bytes(), &destination); err != nil {
		t.Fatal(err)
	}
	stored, err := e.store.GetApplicationStandardLogDestination(context.Background(), org.ID, destination.ID)
	if err != nil || !stored.HasAuthHeader || len(stored.AuthHeaderSealed) == 0 || strings.Contains(string(stored.AuthHeaderSealed), secret) {
		t.Fatalf("credential not sealed: %v", err)
	}
	namespace, plaintext, err := secretbox.OpenBytes(identity, stored.AuthHeaderSealed)
	if err != nil || namespace != appLogDrainSecretSealLabel || string(plaintext) != secret {
		t.Fatalf("destination credential cannot be opened by the existing drain consumer: %v", err)
	}
	for _, path := range []string{"log-destinations", "log-destinations/" + destination.ID} {
		read := e.do(t, http.MethodGet, base+path, nil, nil)
		if read.Code != http.StatusOK || strings.Contains(read.Body.String(), "standard-destination-test-secret") || strings.Contains(read.Body.String(), "auth_header_sealed") {
			t.Fatalf("credential read leak: %d %s", read.Code, read.Body)
		}
	}
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	published := e.do(t, http.MethodPost, base+"publishers", api.CreateApplicationStandardPublisherRequest{Name: "CI", PublicKeyDER: base64.StdEncoding.EncodeToString(der)}, nil)
	if published.Code != http.StatusCreated {
		t.Fatalf("publisher: %d %s", published.Code, published.Body)
	}
	var publisher api.ApplicationStandardPublisher
	if err := json.Unmarshal(published.Body.Bytes(), &publisher); err != nil {
		t.Fatal(err)
	}
	standard := api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(fmt.Sprintf(`{"log_destinations":{"mode":"mandatory","value":[%q]},"trusted_publishers":{"mode":"restricted","value":[%q]}}`, destination.ID, publisher.ID))}
	created := e.do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/application-standards/production-baseline/versions", standard, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("reference publication: %d %s", created.Code, created.Body)
	}
	other := seedSharedOrgWithOwner(t, e, "other-resource-api", "Other", api.PlanPro)
	otherBase := "/v1/orgs/" + other.Slug
	assertProblem(t, e.do(t, http.MethodGet, otherBase+"/application-standard-log-destinations/"+destination.ID, nil, nil), http.StatusNotFound, api.CodeNotFound)
	assertProblem(t, e.do(t, http.MethodGet, otherBase+"/application-standard-publishers/"+publisher.ID, nil, nil), http.StatusNotFound, api.CodeNotFound)
	assertProblem(t, e.do(t, http.MethodPost, otherBase+"/application-standards/foreign-baseline/versions", standard, nil), http.StatusBadRequest, api.CodeValidation)
	assertProblem(t, e.do(t, http.MethodGet, base+"publishers?after=bad", nil, nil), http.StatusBadRequest, api.CodeValidation)
	assertProblem(t, e.do(t, http.MethodPost, base+"publishers", api.CreateApplicationStandardPublisherRequest{Name: "Bad", PublicKeyDER: "private-key-material"}, nil), http.StatusBadRequest, api.CodeValidation)
}
