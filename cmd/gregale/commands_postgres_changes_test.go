// adr: 20261112140000
package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakePostgresChangesClient struct {
	put       []api.PutPostgresChangesRequest
	putErr    error
	response  api.PostgresChangesResponse
	connected []string
}

func (f *fakePostgresChangesClient) PutPostgresChanges(_ context.Context, _ string, req api.PutPostgresChangesRequest) (api.PostgresChangesResponse, error) {
	f.put = append(f.put, req)
	return f.response, f.putErr
}
func (f *fakePostgresChangesClient) GetPostgresChanges(context.Context, string) (api.PostgresChangesResponse, error) {
	return f.response, nil
}
func (f *fakePostgresChangesClient) DeletePostgresChanges(context.Context, string) error { return nil }
func (f *fakePostgresChangesClient) PutPostgresChangesConnection(_ context.Context, _ string, raw string) error {
	f.connected = append(f.connected, raw)
	return nil
}

func writePostgresChangesManifest(t *testing.T, block string) string {
	t.Helper()
	dir := t.TempDir()
	body := "schema_version: 1\nworker:\n  scale: { min: 0, max: 1, metric: queue_depth, target: 100 }\nqueue_bindings:\n  - name: order-changes\n    queue_name: order-changes\n" + block
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDeployManifestPostgresChanges(t *testing.T) {
	ctx := context.Background()
	// No block: nothing is sent.
	none := &fakePostgresChangesClient{}
	if err := deployManifestPostgresChanges(ctx, none, "app", writePostgresChangesManifest(t, "")); err != nil || len(none.put) != 0 {
		t.Fatalf("no block: %v %d", err, len(none.put))
	}

	managed := writePostgresChangesManifest(t, `postgres_changes:
  database: shop
  queue_name: order-changes
  poll_interval: 5m
  feeds: [{name: orders, table: public.orders, columns: [id, status]}]
`)
	client := &fakePostgresChangesClient{response: api.PostgresChangesResponse{Managed: true, Database: "shop", QueueName: "order-changes"}}
	if err := deployManifestPostgresChanges(ctx, client, "app", managed); err != nil {
		t.Fatal(err)
	}
	if len(client.put) != 1 || client.put[0].Database != "shop" || client.put[0].PollIntervalSeconds != 300 {
		t.Fatalf("sent %+v", client.put)
	}

	// Unreleased accounts deploy with a warning.
	dark := &fakePostgresChangesClient{putErr: &api.APIError{Problem: api.Problem{Code: "postgres_changes_unavailable"}}}
	if err := deployManifestPostgresChanges(ctx, dark, "app", managed); err != nil {
		t.Fatalf("unreleased account failed the deploy: %v", err)
	}
	// Other failures fail the deploy.
	broken := &fakePostgresChangesClient{putErr: errors.New("boom")}
	if err := deployManifestPostgresChanges(ctx, broken, "app", managed); err == nil {
		t.Fatal("a failed apply passed the deploy")
	}

	// BYO without the local owner URL declares and waits for install.
	byo := writePostgresChangesManifest(t, `postgres_changes:
  connection_url_env: GREGALE_TEST_ORDERS_URL_UNSET
  queue_name: order-changes
  feeds: [{name: orders, table: orders, columns: [id]}]
`)
	t.Setenv("GREGALE_TEST_ORDERS_URL_UNSET", "")
	pending := &fakePostgresChangesClient{response: api.PostgresChangesResponse{SourceID: "00000000-0000-0000-0000-000000000001"}}
	if err := deployManifestPostgresChanges(ctx, pending, "app", byo); err != nil {
		t.Fatal(err)
	}
	if len(pending.put) != 1 || len(pending.connected) != 0 {
		t.Fatalf("byo without owner url: put=%d connected=%d", len(pending.put), len(pending.connected))
	}
}

func TestRedactPostgresError(t *testing.T) {
	err := redactPostgresError(errors.New(`failed to connect to user=postgres password=s3cr3t host=db`), "postgres://postgres:s3cr3t@db/shop")
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("password leaked: %v", err)
	}
}
