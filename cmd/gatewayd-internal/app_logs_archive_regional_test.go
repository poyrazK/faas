package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestRegionalArchiveReaderOwnershipAndIntegrityAfterInstanceGC(t *testing.T) {
	ctx := t.Context()
	s := state.NewMemStore()
	fx := conformance.Seed(t, s)
	id := "eu-log-reader"
	if _, err := s.UpsertComputeRegion(ctx, state.ComputeRegion{ID: id, DisplayName: id, AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	app := fx.App
	app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "logs-"+uuid.NewString(), "", id
	app, err := s.CreateApp(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64), CommitSHA: strings.Repeat("b", 40), Tag: "archive-tag"})
	if err != nil {
		t.Fatal(err)
	}
	node := fx.Node
	node.ID, node.Name, node.Region = "", "logs-"+uuid.NewString(), &id
	node, err = s.CreateComputeNode(ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := s.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := s.CaptureRuntimeLogArchiveBinding(ctx, instance.ID, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	day := archiveTestDate()
	ref := state.RuntimeLogArchive{RuntimeLogArchiveBinding: binding, Day: day, StorageKey: region.RuntimeLogArchiveKey(id, instance.ID, day)}
	raw := gzipJSONL(t, []string{`{"seq":1,"stream":"stdout","msg":"owned history","ts":"2026-10-02T00:00:00Z"}`})
	digest := sha256.Sum256(raw)
	if err := s.ReserveRuntimeLogArchive(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishRuntimeLogArchive(ctx, instance.ID, day, int64(len(raw)), hex.EncodeToString(digest[:])); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	legacy, _ := storage.NewLocalStorageBackend(t.TempDir())
	eu, _ := storage.NewLocalStorageBackend(t.TempDir())
	router, _ := storage.NewRegionRouter(map[string]storage.StorageBackend{id: eu}, legacy)
	h := &ArchiveLogsHandler{Store: s, Archives: &logarchive.RoutedClient{Regional: router}, Backstop: time.Second}
	run := func(appID, acctID string) string {
		r := httptest.NewRecorder()
		h.serveArchiveWithAccount(ctx, r, r, appID, acctID, instance.ID, day)
		return r.Body.String()
	}
	if err := router.Put(ctx, ref.StorageKey, bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	out := run(app.ID, app.AccountID)
	for _, want := range []string{"owned history", archiveReasonComplete, `"region":"` + id + `"`, `"deployment_id":"` + dep.ID + `"`, `"commit_sha":"` + dep.CommitSHA + `"`, `"deployment_tag":"archive-tag"`, `"image_digest":"` + dep.ImageDigest + `"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	for _, owner := range [][2]string{{fx.App.ID, app.AccountID}, {app.ID, uuid.NewString()}} {
		out := run(owner[0], owner[1])
		if strings.Contains(out, "event: log") || !strings.Contains(out, archiveReasonMissing) {
			t.Fatalf("foreign history leaked: %s", out)
		}
	}
	for name, body := range map[string][]byte{"replaced": gzipJSONL(t, []string{`{"seq":1,"msg":"replacement"}`}), "truncated": raw[:len(raw)-5], "oversized": append(append([]byte{}, raw...), raw...)} {
		t.Run(name, func(t *testing.T) {
			if err := router.Put(ctx, ref.StorageKey, bytes.NewReader(body)); err != nil {
				t.Fatal(err)
			}
			out := run(app.ID, app.AccountID)
			if !strings.Contains(out, archiveReasonDegraded) || strings.Contains(out, archiveReasonComplete) {
				t.Fatalf("corruption accepted: %s", out)
			}
		})
	}
	revoked, _ := storage.NewRegionRouter(map[string]storage.StorageBackend{}, legacy)
	h.Archives = &logarchive.RoutedClient{Regional: revoked}
	if out := run(app.ID, app.AccountID); !strings.Contains(out, archiveReasonDegraded) || strings.Contains(out, "event: log") {
		t.Fatalf("revoked route read: %s", out)
	}
	r, err := legacy.Get(ctx, region.RuntimeLogArchiveKey("", instance.ID, day))
	if r != nil {
		_ = r.Close()
	}
	if !storage.IsNotFound(err) {
		t.Fatalf("legacy store touched: %v", err)
	}
}

func TestLegacyArchiveReaderRejectsUnownedInstanceBeforeStorage(t *testing.T) {
	s := state.NewMemStore()
	fx := conformance.Seed(t, s)
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = io.WriteString(w, "should not be read")
	}))
	defer server.Close()
	client := newArchiveTestClient(t, server)
	h := &ArchiveLogsHandler{Store: s, S3: client, Bucket: "test-bucket"}
	r := httptest.NewRecorder()
	h.serveArchiveWithAccount(t.Context(), r, r, fx.App.ID, fx.Account.ID, uuid.NewString(), archiveTestDate())
	if called || !strings.Contains(r.Body.String(), archiveReasonMissing) {
		t.Fatalf("unowned legacy instance reached store: called=%v body=%s", called, r.Body.String())
	}
}

func TestLegacyArchiveKeepsPhysicalNodeRegion(t *testing.T) {
	s := state.NewMemStore()
	fx := conformance.Seed(t, s)
	ctx := t.Context()
	id := "physical-eu"
	if _, err := s.UpsertComputeRegion(ctx, state.ComputeRegion{ID: id, DisplayName: id, AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	node := fx.Node
	node.ID, node.Name, node.Region = "", "legacy-logs-"+uuid.NewString(), &id
	node, err := s.CreateComputeNode(ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := s.CreateInstance(ctx, fx.App.ID, fx.Deployment.ID, string(state.StateRunning), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := s.CaptureRuntimeLogArchiveBinding(ctx, instance.ID, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	day := archiveTestDate()
	raw := gzipJSONL(t, []string{`{"seq":1,"msg":"legacy history"}`})
	digest := sha256.Sum256(raw)
	ref := state.RuntimeLogArchive{RuntimeLogArchiveBinding: binding, Day: day, StorageKey: region.RuntimeLogArchiveKey("", instance.ID, day)}
	if err := s.ReserveRuntimeLogArchive(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishRuntimeLogArchive(ctx, instance.ID, day, int64(len(raw)), hex.EncodeToString(digest[:])); err != nil {
		t.Fatal(err)
	}
	server := newFakeS3(t, http.StatusOK, raw)
	h := &ArchiveLogsHandler{Store: s, S3: newArchiveTestClient(t, server), Bucket: "test-bucket"}
	r := httptest.NewRecorder()
	h.serveArchiveWithAccount(ctx, r, r, fx.App.ID, fx.Account.ID, instance.ID, day)
	out := r.Body.String()
	if !strings.Contains(out, `"region":"physical-eu"`) || !strings.Contains(out, archiveReasonComplete) {
		t.Fatalf("legacy physical region lost: %s", out)
	}
}
