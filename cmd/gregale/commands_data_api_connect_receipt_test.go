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

type connectRemoteFixture struct {
	app     api.AppResponse
	db      api.ManagedPostgresDatabase
	binding api.ManagedPostgresBinding
	review  api.AppReleaseReview
}

func (f *connectRemoteFixture) GetApp(context.Context, string) (api.AppResponse, error) {
	return f.app, nil
}
func (f *connectRemoteFixture) GetManagedPostgresDatabase(context.Context, string) (api.ManagedPostgresDatabase, error) {
	return f.db, nil
}
func (f *connectRemoteFixture) ListManagedPostgresBindings(context.Context, string) (api.ManagedPostgresBindingList, error) {
	return api.ManagedPostgresBindingList{Items: []api.ManagedPostgresBinding{f.binding}}, nil
}
func (f *connectRemoteFixture) GetAppReleaseReview(context.Context, string) (api.AppReleaseReview, error) {
	return f.review, nil
}

func connectReceiptFixture(t *testing.T) (dataAPIConnectReceipt, *connectRemoteFixture) {
	t.Helper()
	hash := strings.Repeat("a", 64)
	r := dataAPIConnectReceipt{Version: 1, App: "notes", APIHash: hash, DatabaseID: "db-1", AppID: "app-1", BindingID: "binding-1", CredentialGeneration: 1, DeploymentID: "deployment-1", CollectionHash: hash, Output: filepath.Join(t.TempDir(), "types.ts"), NumericMode: "number", Stage: "creating"}
	f := &connectRemoteFixture{app: api.AppResponse{ID: "app-1", Slug: "notes"}, db: api.ManagedPostgresDatabase{ID: "db-1", State: "ready"}, binding: api.ManagedPostgresBinding{ID: "binding-1", DatabaseID: "db-1", AppID: "app-1", Scope: api.DefaultEnvScope, EnvironmentKey: "DATABASE_URL", Access: "read_only", State: "ready", CredentialGeneration: 1}, review: api.AppReleaseReview{AppID: "app-1", Scope: api.DefaultEnvScope, ConfigurationHash: hash, LatestDeploymentID: "deployment-1", ServingDeploymentIDs: []string{"deployment-1"}}}
	return r, f
}

func TestDataAPIConnectReceiptPersistsPrivateProgressWithoutOverwriting(t *testing.T) {
	r, f := connectReceiptFixture(t)
	if err := dataAPIConnectSnapshot(context.Background(), f, &r, true); err != nil {
		t.Fatal(err)
	}
	r.Stage = "deployed"
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := writeDataAPIConnectReceipt(path, r, true); err != nil {
		t.Fatal(err)
	}
	if err := writeDataAPIConnectReceipt(path, r, true); err == nil {
		t.Fatal("receipt was replaced")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%v err=%v", info, err)
	}
	got, err := readDataAPIConnectReceipt(path)
	if err != nil || got.AppID != r.AppID || got.DeploymentID != r.DeploymentID {
		t.Fatalf("receipt=%+v err=%v", got, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"JWT", "issuer", "password", "request_body", "identities"} {
		if strings.Contains(strings.ToLower(string(data)), strings.ToLower(forbidden)) {
			t.Fatalf("receipt contains %q", forbidden)
		}
	}
}

func TestDataAPIConnectSnapshotRejectsChangedOwnershipAndDeployment(t *testing.T) {
	for _, change := range []struct {
		name   string
		mutate func(*connectRemoteFixture)
	}{
		{"replaced app", func(f *connectRemoteFixture) { f.app.ID = "different" }},
		{"database held", func(f *connectRemoteFixture) { f.db.Hold = "inactive" }},
		{"binding changed", func(f *connectRemoteFixture) { f.binding.ID = "different" }},
		{"credentials rotated", func(f *connectRemoteFixture) { f.binding.CredentialGeneration++ }},
		{"rotation pending", func(f *connectRemoteFixture) { f.binding.RotationPending = true }},
		{"wrong access", func(f *connectRemoteFixture) { f.binding.Access = "read_write" }},
		{"configuration changed", func(f *connectRemoteFixture) { f.review.ConfigurationHash = strings.Repeat("b", 64) }},
		{"deployment changed", func(f *connectRemoteFixture) {
			f.review.LatestDeploymentID = "different"
			f.review.ServingDeploymentIDs = []string{"different"}
		}},
		{"mixed traffic", func(f *connectRemoteFixture) {
			f.review.ServingDeploymentIDs = append(f.review.ServingDeploymentIDs, "other")
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			r, f := connectReceiptFixture(t)
			if err := dataAPIConnectSnapshot(context.Background(), f, &r, true); err != nil {
				t.Fatal(err)
			}
			r.Stage = "deployed"
			if err := dataAPIConnectSnapshot(context.Background(), f, &r, false); err != nil {
				t.Fatal(err)
			}
			change.mutate(f)
			if err := dataAPIConnectSnapshot(context.Background(), f, &r, false); err == nil {
				t.Fatal("changed resources accepted")
			}
		})
	}
}

func TestDataAPIConnectSnapshotNeverAdoptsDifferentCreatedResources(t *testing.T) {
	for _, change := range []func(*connectRemoteFixture){
		func(f *connectRemoteFixture) { f.app.ID = "other"; f.binding.AppID = "other"; f.review.AppID = "other" },
		func(f *connectRemoteFixture) { f.binding.ID = "other" },
		func(f *connectRemoteFixture) {
			f.review.LatestDeploymentID = "other"
			f.review.ServingDeploymentIDs = []string{"other"}
		},
	} {
		r, f := connectReceiptFixture(t)
		change(f)
		if err := dataAPIConnectSnapshot(context.Background(), f, &r, true); err == nil {
			t.Fatal("adopted a replacement resource")
		}
	}
}

func TestConnectedAuthorizationRequiresStableResourcesAroundPinnedReplay(t *testing.T) {
	for _, tc := range []struct {
		name      string
		change    func(*connectRemoteFixture)
		replayErr error
		want      string
	}{
		{name: "success", want: "passed"},
		{name: "replay failure", replayErr: errors.New("failed"), want: "failed_or_unavailable"},
		{name: "deployment changed", change: func(f *connectRemoteFixture) {
			f.review.LatestDeploymentID = "other"
			f.review.ServingDeploymentIDs = []string{"other"}
		}, want: "stale_or_unavailable"},
		{name: "endpoint changed", change: func(f *connectRemoteFixture) { f.app.URL = "https://other.example" }, want: "stale_or_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, f := connectReceiptFixture(t)
			f.app.URL = "https://notes.example"
			if err := dataAPIConnectSnapshot(context.Background(), f, &r, true); err != nil {
				t.Fatal(err)
			}
			r.Stage = "verified"
			r.Fingerprint = strings.Repeat("b", 64)
			calls := 0
			status := checkConnectedAuthorization(context.Background(), f, r, func(_ context.Context, endpoint, fingerprint string) error {
				calls++
				if endpoint != "https://notes.example/__gregale/ready" || fingerprint != r.Fingerprint {
					t.Fatalf("wrong replay target: %s %s", endpoint, fingerprint)
				}
				if tc.change != nil {
					tc.change(f)
				}
				return tc.replayErr
			})
			if status != tc.want || calls != 1 {
				t.Fatalf("status=%s calls=%d", status, calls)
			}
		})
	}
}

func TestConnectedAuthorizationDoesNotReplayStaleResources(t *testing.T) {
	r, f := connectReceiptFixture(t)
	f.app.URL = "https://notes.example"
	if err := dataAPIConnectSnapshot(context.Background(), f, &r, true); err != nil {
		t.Fatal(err)
	}
	r.Stage = "verified"
	f.binding.CredentialGeneration++
	status := checkConnectedAuthorization(context.Background(), f, r, func(context.Context, string, string) error { t.Fatal("replayed a stale connection"); return nil })
	if status != "stale_or_unavailable" {
		t.Fatalf("status=%s", status)
	}
}

func TestDataAPIConnectSnapshotUsesServingDeployment(t *testing.T) {
	receipt, remote := connectReceiptFixture(t)
	remote.review.LatestDeploymentID = "newer-failed"
	if err := dataAPIConnectSnapshot(context.Background(), remote, &receipt, true); err != nil {
		t.Fatal(err)
	}
	if err := dataAPIConnectSnapshot(context.Background(), remote, &receipt, false); err != nil {
		t.Fatal(err)
	}
}
