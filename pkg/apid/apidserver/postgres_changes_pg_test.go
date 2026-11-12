// adr: 20261112140000
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/sealdomain"
)

func TestPostgresChangesAPIBringYourOwnDatabase(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	previous, previousMode := *apidserver.DeliveryDomainRecipient, *apidserver.DeliveryWriteMode
	*apidserver.DeliveryDomainRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	*apidserver.DeliveryWriteMode = sealdomain.WriteDomain
	t.Cleanup(func() { *apidserver.DeliveryDomainRecipient, *apidserver.DeliveryWriteMode = previous, previousMode })
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	if res := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "orders-worker"}, nil); res.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", res.Code, res.Body.String())
	}
	path := "/v1/apps/orders-worker/postgres-changes"
	req := api.PutPostgresChangesRequest{QueueName: "order-changes", PollIntervalSeconds: 300,
		Feeds: []api.PostgresChangeFeed{{Name: "orders", Table: "public.orders", Columns: []string{"id", "status"}}}}

	// Dark until the platform flag releases it.
	if res := e.Do(t, http.MethodPut, path, req, nil); res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "postgres_changes_unavailable") {
		t.Fatalf("unreleased: %d %s", res.Code, res.Body.String())
	}
	(*e.S()).EnablePlatformFlagForAll(platformflags.PostgresChanges)

	bad := req
	bad.Feeds = []api.PostgresChangeFeed{{Name: "Orders", Table: "orders", Columns: []string{"id"}}}
	if res := e.Do(t, http.MethodPut, path, bad, nil); res.Code != http.StatusBadRequest && res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid feed accepted: %d %s", res.Code, res.Body.String())
	}

	res := e.Do(t, http.MethodPut, path, req, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("put: %d %s", res.Code, res.Body.String())
	}
	var got api.PostgresChangesResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Managed || got.Connected || !got.Enabled || got.QueueName != "order-changes" || got.PollIntervalSeconds != 300 ||
		len(got.Feeds) != 1 || !strings.HasPrefix(got.RelayRole, "gregale_changes_") {
		t.Fatalf("response %+v", got)
	}
	// Re-declaring is idempotent: same source.
	again := e.Do(t, http.MethodPut, path, req, nil)
	var second api.PostgresChangesResponse
	if err := json.Unmarshal(again.Body.Bytes(), &second); err != nil || second.SourceID != got.SourceID {
		t.Fatalf("redeclare moved the source: %+v (%v)", second, err)
	}

	conn := path + "/connection"
	owner := map[string]string{"connection_url": "postgres://postgres:secret@db.example.com/shop?sslmode=verify-full"}
	if res := e.Do(t, http.MethodPut, conn, owner, nil); res.Code != http.StatusBadRequest || strings.Contains(res.Body.String(), "secret") {
		t.Fatalf("owner credential: %d %s", res.Code, res.Body.String())
	}
	insecure := map[string]string{"connection_url": "postgres://" + got.RelayRole + ":pw@db.example.com/shop?sslmode=require"}
	if res := e.Do(t, http.MethodPut, conn, insecure, nil); res.Code != http.StatusBadRequest {
		t.Fatalf("unverified TLS: %d %s", res.Code, res.Body.String())
	}
	relay := map[string]string{"connection_url": "postgres://" + got.RelayRole + ":pw@db.example.com/shop?sslmode=verify-full"}
	if res := e.Do(t, http.MethodPut, conn, relay, nil); res.Code != http.StatusNoContent {
		t.Fatalf("relay credential: %d %s", res.Code, res.Body.String())
	}
	status := e.Do(t, http.MethodGet, path, nil, nil)
	var connected api.PostgresChangesResponse
	if err := json.Unmarshal(status.Body.Bytes(), &connected); err != nil || !connected.Connected {
		t.Fatalf("status after connect: %s (%v)", status.Body.String(), err)
	}
	if strings.Contains(status.Body.String(), "pw@") {
		t.Fatal("status returned credential material")
	}

	if res := e.Do(t, http.MethodDelete, path, nil, nil); res.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", res.Code, res.Body.String())
	}
	paused := e.Do(t, http.MethodGet, path, nil, nil)
	var after api.PostgresChangesResponse
	if err := json.Unmarshal(paused.Body.Bytes(), &after); err != nil || after.Enabled {
		t.Fatalf("delete did not pause: %s (%v)", paused.Body.String(), err)
	}
	// Declaring again resumes.
	if res := e.Do(t, http.MethodPut, path, req, nil); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"enabled":true`) {
		t.Fatalf("resume: %d %s", res.Code, res.Body.String())
	}
}

func TestPostgresChangesAPIFreePlanRefused(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanFree)
	(*e.S()).EnablePlatformFlagForAll(platformflags.PostgresChanges)
	if res := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "free-worker"}, nil); res.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", res.Code, res.Body.String())
	}
	req := api.PutPostgresChangesRequest{QueueName: "q", Feeds: []api.PostgresChangeFeed{{Name: "o", Table: "o", Columns: []string{"id"}}}}
	if res := e.Do(t, http.MethodPut, "/v1/apps/free-worker/postgres-changes", req, nil); res.Code != http.StatusPaymentRequired && res.Code != http.StatusForbidden {
		t.Fatalf("free plan: %d %s", res.Code, res.Body.String())
	}
}
