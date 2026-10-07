package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 628
// Opt-in local API/gateway and built CLI qualification against disposable GCS.
// The caller owns fixture provisioning, IAM, the CLI binary and cloud cleanup.
func TestGCSLiveCLIQualification(t *testing.T) {
	if os.Getenv("FAAS_GCS_FEATURE_CLI_LIVE") != "1" {
		t.Skip("live-test opt-in required")
	}
	root := os.Getenv("FAAS_GCS_FEATURE_FIXTURE_DIR")
	var manifest struct {
		TestBuckets []string `json:"test_buckets"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &manifest); err != nil || len(manifest.TestBuckets) != 2 {
		t.Fatal("invalid fixture manifest")
	}
	for _, b := range manifest.TestBuckets {
		if !strings.HasPrefix(b, "gregale-cli-") {
			t.Fatal("unsafe fixture bucket")
		}
	}
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	prevR, prevI, prevH := setSecretRecipient, mfaIdentities, hostHMACKey
	setSecretRecipient = func() *age.X25519Recipient { return ident.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{ident} }
	hmac := make([]byte, 32)
	if _, err = rand.Read(hmac); err != nil {
		t.Fatal(err)
	}
	hostHMACKey = func() []byte { return hmac }
	t.Cleanup(func() { setSecretRecipient, mfaIdentities, hostHMACKey = prevR, prevI, prevH })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	st := state.NewMemStore()
	acct, err := st.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	bearer, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateAPIKey(ctx, acct.ID, hash, "local-qualification", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := newServer(st, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", nil)
	var gateway http.Handler
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gateway.ServeHTTP(w, r) }))
	defer public.Close()
	var cfg objectstorage.Config
	raw, err = os.ReadFile(filepath.Join(root, "provider-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Backends) != 1 || cfg.Backends[0].Namespace != "gregale-storage-prod-280686" || !strings.HasPrefix(cfg.Backends[0].GCSServiceAccount, "gregale-qual-") {
		t.Fatal("isolated provider identity required")
	}
	since := time.Now().UTC().Add(-time.Second)
	cfg.PublicEndpoint = strings.Replace(public.URL, "http://", "https://", 1)
	cfg.MaxSinglePutBytes = 1 << 20
	cfg.MaxPartBytes = api.MinMultipartPartBytes
	cfg.Transfer = objectstorage.ObjectTransferConfig{Profile: "proxied"}
	cfg.Accounting = &api.ObjectStoragePolicy{AccountingMode: api.ObjectStorageGatewaySafetyV1, GatewayMeteringSince: &since, MaxAccountBytes: 64 << 20, MaxBucketBytes: 32 << 20, MaxAccountKeys: 100, MaxMonthlyRequests: 2000, MaxMonthlyEgressBytes: 64 << 20, MaxMonthlyAuthorizations: 2000, MaxReportAgeSeconds: 900}
	registry, err := objectstorage.NewRegistry(cfg, os.Getenv, map[string]objectstorage.Factory{"gcs": objectstorage.NewGCS})
	if err != nil {
		t.Fatal(err)
	}
	srv.WithObjectStorage(registry)
	if err = srv.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default(cfg.DefaultRegion)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err = s3gateway.New(s3gateway.Config{Registry: registry, Store: st, RequestMetrics: st, SpoolDir: t.TempDir(), MinSpoolFreeBytes: 1 << 20, OpenSecret: func(blob []byte) (string, error) {
		ns, plain, e := secretbox.OpenBytes(ident, blob)
		if e != nil || ns != s3gateway.CredentialSecretNamespace {
			return "", errors.New("invalid fixture credential")
		}
		return string(plain), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	// The CLI permits HTTP only on loopback. Rewrite the local capability's
	// scheme after issuance: its token/host/path stay intact, and the native
	// GCS connection still uses verified TLS. No host trust store is changed.
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured := httptest.NewRecorder()
		srv.handler().ServeHTTP(captured, r)
		for name, values := range captured.Header() {
			w.Header()[name] = values
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(captured.Code)
		_, _ = w.Write(bytes.ReplaceAll(captured.Body.Bytes(), []byte(cfg.PublicEndpoint), []byte(public.URL)))
	}))
	defer control.Close()
	client := api.NewClient(control.URL, bearer)
	app, err := client.CreateApp(ctx, api.CreateAppRequest{Slug: "cli-gcs-qualification"})
	if err != nil {
		t.Fatal(err)
	}
	buckets := make([]state.ObjectBucket, 0, 2)
	for i, name := range []string{"assets", "source"} {
		b, e := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID, Name: name, Scope: "default", Region: cfg.DefaultRegion, PhysicalName: manifest.TestBuckets[i], BackendID: backend.ID, BackendFingerprint: backend.Fingerprint}, 10)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = st.ClaimObjectBucket(ctx, acct.ID, app.ID, b.ID, "fixture", "provisioning"); e != nil {
			t.Fatal(e)
		}
		if e = st.FinishObjectBucket(ctx, b.ID, "fixture", "ready"); e != nil {
			t.Fatal(e)
		}
		b.State = "ready"
		buckets = append(buckets, b)
		if e = st.ClaimObjectInventory(ctx, b.ID, "initial"); e != nil {
			t.Fatal(e)
		}
		if e = srv.scanObjectInventory(ctx, st, b, "initial"); e != nil {
			t.Fatal("real initial GCS inventory failed", e)
		}
	}
	// Pre-created native buckets have a settled disabled versioning policy.
	// Advance only the isolated memory journal's propagation clock; native
	// reads and the complete generation inventories still run against GCS.
	settled := time.Now().UTC().Add(-20 * time.Minute)
	st.SetClockForTest(func() time.Time { return settled })
	for _, b := range buckets {
		if _, err := st.ObserveObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Suspended"); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.reconcileObjectBucketVersioning(ctx, nil); err != nil {
		t.Fatal(err)
	}
	propagated := time.Now().UTC().Add(-4 * time.Minute)
	st.SetClockForTest(func() time.Time { return propagated })
	if err := srv.reconcileObjectBucketVersioning(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := srv.reconcileObjectCapacity(ctx, nil); err != nil {
		t.Fatal(err)
	}
	st.SetClockForTest(time.Now)
	if err := srv.reconcileObjectBucketVersioning(ctx, nil); err != nil {
		t.Fatal(err)
	}

	cli := os.Getenv("FAAS_GCS_FEATURE_CLI_LIVE_BIN")
	if cli == "" {
		t.Fatal("built CLI required")
	}
	run := func(t *testing.T, args ...string) map[string]json.RawMessage {
		t.Helper()
		command := exec.CommandContext(ctx, cli, append([]string{"--json"}, args...)...)
		command.Dir = t.TempDir()
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "FAAS_") && !strings.HasPrefix(value, "GREGALE_") {
				command.Env = append(command.Env, value)
			}
		}
		command.Env = append(command.Env, "FAAS_API="+control.URL, "FAAS_TOKEN="+bearer)
		output, err := command.Output()
		if err != nil {
			var failure *exec.ExitError
			if errors.As(err, &failure) {
				for _, b := range buckets {
					pending, _, _ := st.ListObjectMultipartUploads(ctx, b.AccountID, b.AppID, b.ID, 100, "")
					for _, u := range pending {
						t.Logf("multipart journal state: %s; geometry %d bytes/%d parts", u.State, u.PartSizeBytes, u.PartCount)
					}
				}
				// CLI errors sanitize capability URLs and contain no bearer values.
				t.Fatalf("CLI %s failed: %s", strings.Join(args[:min(2, len(args))], " "), failure.Stderr)
			}
			t.Fatal("CLI process failed")
		}
		var result map[string]json.RawMessage
		if err := json.Unmarshal(output, &result); err != nil {
			t.Fatal("CLI result was not JSON")
		}
		return result
	}
	b := buckets[0]
	rebase := func(t *testing.T) {
		t.Helper()
		job := run(t, "bucket", "reconcile", "start", app.Slug, b.ID)
		var id string
		_ = json.Unmarshal(job["id"], &id)
		if err := srv.reconcileObjectCapacity(ctx, nil); err != nil {
			t.Fatal(err)
		}
		status := run(t, "bucket", "reconcile", "status", app.Slug, b.ID, id)
		if string(status["state"]) != `"completed"` {
			t.Fatal("native inventory did not complete")
		}
	}
	if !t.Run("native bucket controls and copy grant CLI", func(t *testing.T) {
		run(t, "bucket", "versioning", "status", app.Slug, b.ID)
		run(t, "bucket", "encryption", "status", app.Slug, b.ID)
		run(t, "bucket", "encryption", "AES256", app.Slug, b.ID)
		if err := srv.reconcileObjectBucketEncryption(ctx, nil); err != nil {
			t.Fatal(err)
		}
		run(t, "bucket", "encryption", "clear", app.Slug, b.ID)
		if err := srv.reconcileObjectBucketEncryption(ctx, nil); err != nil {
			t.Fatal(err)
		}
		credential, err := client.CreateObjectS3Credential(ctx, app.Slug, b.ID, api.CreateObjectS3CredentialRequest{Label: "copy-grant", Permission: "read_write"})
		if err != nil {
			t.Fatal(err)
		}
		run(t, "bucket", "copy-sources", "grant", app.Slug, b.ID, credential.ID, buckets[1].ID, "qualification/")
		run(t, "bucket", "copy-sources", "list", app.Slug, b.ID, credential.ID)
		run(t, "bucket", "copy-sources", "revoke", app.Slug, b.ID, credential.ID, buckets[1].ID)
	}) {
		return
	}
	rebase(t)
	for _, tc := range []struct {
		name string
		size int
	}{{"single", 47}, {"automatic multipart", int(api.MinMultipartPartBytes) + 3}} {
		if !t.Run(tc.name, func(t *testing.T) {
			payload := bytes.Repeat([]byte("x"), tc.size)
			source, target := filepath.Join(t.TempDir(), "source"), filepath.Join(t.TempDir(), "download")
			if err := os.WriteFile(source, payload, 0600); err != nil {
				t.Fatal(err)
			}
			key := "qualification/" + tc.name + "/世界 +%.txt"
			result := run(t, "bucket", "upload", app.Slug, b.ID, key, source)
			if string(result["status"]) != `"completed"` {
				t.Fatal("transfer did not complete")
			}
			var id string
			_ = json.Unmarshal(result["upload_id"], &id)
			if id == "" {
				t.Fatal("owned upload ID absent")
			}
			if tc.size < 1<<20 {
				run(t, "bucket", "writes", "status", app.Slug, b.ID, id)
				run(t, "bucket", "writes", "wait", app.Slug, b.ID, id, "--timeout=3s", "--poll-interval=1s")
			} else {
				run(t, "bucket", "uploads", "status", app.Slug, b.ID, id)
			}
			rebase(t)
			run(t, "bucket", "download", app.Slug, b.ID, key, target)
			got, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatal("download changed native bytes", err)
			}
			deletion := run(t, "bucket", "deletions", "start", app.Slug, b.ID, key, uuid.NewString())
			if string(deletion["state"]) != `"completed"` {
				t.Fatal("native current deletion did not complete")
			}
			rebase(t)
		}) {
			return
		}
	}
	// adr: 638
	if !t.Run("CLI immutable version history", func(t *testing.T) {
		past := time.Now().UTC().Add(-20 * time.Minute)
		st.SetClockForTest(func() time.Time { return past })
		defer st.SetClockForTest(time.Now)
		run(t, "bucket", "versioning", "enable", app.Slug, b.ID)
		if err := srv.reconcileObjectBucketVersioning(ctx, nil); err != nil {
			t.Fatal(err)
		}
		propagated := time.Now().UTC().Add(-4 * time.Minute)
		st.SetClockForTest(func() time.Time { return propagated })
		if err := srv.reconcileObjectBucketVersioning(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if err := srv.reconcileObjectCapacity(ctx, nil); err != nil {
			t.Fatal(err)
		}
		st.SetClockForTest(time.Now)
		if err := srv.reconcileObjectBucketVersioning(ctx, nil); err != nil {
			t.Fatal(err)
		}
		// Real GCS requires propagation before replacing a versioned object.
		select {
		case <-time.After(31 * time.Second):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		key := "qualification/versioned/世界 +%.txt"
		source := filepath.Join(t.TempDir(), "source")
		payloads := []string{"old native generation", "replacement native generation"}
		var ids []string
		for _, payload := range payloads {
			if err := os.WriteFile(source, []byte(payload), 0600); err != nil {
				t.Fatal(err)
			}
			out := run(t, "bucket", "upload", app.Slug, b.ID, key, source)
			var id string
			if json.Unmarshal(out["version_id"], &id) != nil || !state.ValidObjectVersionID(id) || id == "null" {
				t.Fatal("immutable upload version absent")
			}
			ids = append(ids, id)
		}
		if ids[0] == ids[1] {
			t.Fatal("overwrites collapsed")
		}
		rebase(t)
		list := func(args ...string) api.ObjectVersionList {
			t.Helper()
			out := run(t, append([]string{"bucket", "versions", "list", app.Slug, b.ID}, args...)...)
			raw, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			var page api.ObjectVersionList
			if err = json.Unmarshal(raw, &page); err != nil {
				t.Fatal(err)
			}
			return page
		}
		first := list("--prefix="+key, "--limit=1")
		if len(first.Items) != 1 || first.NextKeyMarker != key || first.NextVersionIDMarker == "" {
			t.Fatal("first public page incomplete", first)
		}
		second := list("--prefix="+key, "--limit=1", "--key-marker="+first.NextKeyMarker, "--version-id-marker="+first.NextVersionIDMarker)
		if len(second.Items) != 1 || second.NextKeyMarker != "" {
			t.Fatal("continuation incomplete", second)
		}
		seen := map[string]bool{}
		for _, version := range []api.ObjectVersion{first.Items[0], second.Items[0]} {
			if version.Key != key || seen[version.VersionID] || version.DeleteMarker || version.VersionID != ids[0] && version.VersionID != ids[1] || version.IsLatest != (version.VersionID == ids[1]) {
				t.Fatal("public history identity changed", version)
			}
			seen[version.VersionID] = true
		}
		grouped := list("--prefix=qualification/", "--delimiter=/", "--limit=10")
		if len(grouped.Items) != 0 || len(grouped.CommonPrefixes) != 1 || grouped.CommonPrefixes[0] != "qualification/versioned/" {
			t.Fatal("public delimiter grouping changed", grouped)
		}
		for i, id := range ids {
			target := filepath.Join(t.TempDir(), "historical")
			out := run(t, "bucket", "download", app.Slug, b.ID, key, target, "--version-id="+id)
			data, err := os.ReadFile(target)
			if err != nil || string(data) != payloads[i] || string(out["version_id"]) != `"`+id+`"` {
				t.Fatal("historical CLI download substituted generation", err)
			}
		}
		deleted := run(t, "bucket", "version-delete", app.Slug, b.ID, key, ids[0])
		if string(deleted["version_id"]) != `"`+ids[0]+`"` {
			t.Fatal("wrong version deletion")
		}
		remaining := list("--prefix="+key, "--limit=10")
		if len(remaining.Items) != 1 || remaining.Items[0].VersionID != ids[1] {
			t.Fatal("exact deletion changed replacement", remaining)
		}
		rebase(t)
		target := filepath.Join(t.TempDir(), "replacement")
		run(t, "bucket", "download", app.Slug, b.ID, key, target, "--version-id="+ids[1])
		if data, err := os.ReadFile(target); err != nil || string(data) != payloads[1] {
			t.Fatal("replacement changed after old generation deletion", err)
		}
	}) {
		return
	}
	t.Run("universal gateway usage CLI", func(t *testing.T) {
		run(t, "usage", "object-storage")
		usage, err := client.GetObjectStorageUsage(ctx)
		if err != nil || !usage.Usage.Fresh || usage.Usage.RequestCount < 10 || usage.Usage.EgressBytes < api.MinMultipartPartBytes || usage.Charges != nil {
			t.Fatal("native gateway usage was not observed", err)
		}
	})
}
