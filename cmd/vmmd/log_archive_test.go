package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestVMMDLogArchiveSink_EnqueueDrainsToSpool(t *testing.T) {
	root := t.TempDir()
	spool := logarchive.NewSpool(filepath.Join(root, "archive"), 1<<20)
	sink := newVMMDLogArchiveSink(spool, logarchive.NewMetrics(nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	sink.Enqueue("instance-1", logbuf.Line{
		Seq:       7,
		Stream:    "stderr",
		Line:      "hello from vmmd",
		WrittenAt: time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC),
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := sink.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	files := spool.FilesSnapshot()
	if len(files) != 1 {
		t.Fatalf("spool files = %d, want 1", len(files))
	}
	body, err := os.ReadFile(files[0].Path)
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	if !strings.Contains(string(body), `"seq":7`) || !strings.Contains(string(body), "hello from vmmd") {
		t.Fatalf("spool body = %s", body)
	}
}

func TestVMMDLogArchiveSink_PersistsResolvedIdentity(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "archive@example.test", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "archive-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	createdAt := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		ID: "dep-1", AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive,
		CommitSHA: "abc123", Tag: "stable", ImageDigest: "sha256:deadbeef", CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	region := "eu-fsn1"
	if _, err := store.CreateComputeNode(ctx, state.ComputeNode{ID: "node-1", Name: "node-1", Active: true, Region: &region}); err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateRunning), 256, "node-1", "wake-1")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	root := t.TempDir()
	spool := logarchive.NewSpool(filepath.Join(root, "archive"), 1<<20)
	sink := newVMMDLogArchiveSinkWithIdentity(spool, logarchive.NewMetrics(nil), slog.New(slog.NewTextHandler(io.Discard, nil)), store, "node-fallback")
	sink.Enqueue(instance.ID, logbuf.Line{Seq: 7, Stream: "stdout", Line: "hello", WrittenAt: time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)})
	closeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := sink.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	files := spool.FilesSnapshot()
	if len(files) != 1 {
		t.Fatalf("spool files = %d, want 1", len(files))
	}
	body, err := os.ReadFile(files[0].Path)
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	var got struct {
		IdentityCaptured bool   `json:"identity_captured"`
		AppID            string `json:"app_id"`
		TenantID         string `json:"tenant_id"`
		DeploymentID     string `json:"deployment_id"`
		NodeID           string `json:"node_id"`
		Region           string `json:"region"`
		CommitSHA        string `json:"commit_sha"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(body), &got); err != nil {
		t.Fatalf("decode spool line: %v", err)
	}
	if !got.IdentityCaptured || got.AppID != app.ID || got.TenantID != account.ID || got.DeploymentID != deployment.ID || got.NodeID != "node-1" || got.Region != region || got.CommitSHA != "abc123" {
		t.Fatalf("persisted identity = %+v", got)
	}
}
