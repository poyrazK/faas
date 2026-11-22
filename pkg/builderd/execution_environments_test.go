package builderd_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderd"
	"github.com/onebox-faas/faas/pkg/runenv"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type environmentVM struct {
	t       *testing.T
	root    string
	request builderd.VMRequest
	waited  bool
}

func (v *environmentVM) Spawn(_ context.Context, r builderd.VMRequest) (builderd.BuildHandle, error) {
	v.request = r
	if r.Framework != builderd.FrameworkDocker || r.RAMMB != api.BuildVMRAMMB || r.KeepWarm || r.Plan != "pro" {
		v.t.Fatalf("unsafe build envelope: %+v", r)
	}
	body, err := os.ReadFile(r.SourcePath)
	if err != nil {
		v.t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != r.SourceSHA256 {
		v.t.Fatal("source digest mismatch")
	}
	return builderd.BuildHandle{BuildID: r.BuildID}, nil
}
func (v *environmentVM) WaitForCompletion(_ context.Context, h builderd.BuildHandle) (builderd.BuildOutcome, error) {
	v.waited = true
	dir := filepath.Join(v.root, h.BuildID)
	if err := os.Mkdir(dir, 0700); err != nil {
		v.t.Fatal(err)
	}
	image := filepath.Join(dir, "build", "out", "image.tar")
	if err := os.MkdirAll(filepath.Dir(image), 0700); err != nil {
		v.t.Fatal(err)
	}
	if err := os.WriteFile(image, []byte("builder-export"), 0600); err != nil {
		v.t.Fatal(err)
	}
	return builderd.BuildOutcome{BuildID: h.BuildID, ExportDir: dir, OCIImage: image}, nil
}
func (v *environmentVM) Cancel(context.Context, string) error { return nil }
func TestEnvironmentBuildUsesIsolatedSharedSlotAndDurableExport(t *testing.T) {
	testEnvironmentBuildUsesIsolatedSharedSlotAndDurableExport(t, api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, Requirements: "six==1.17.0 --hash=sha256:" + strings.Repeat("a", 64)})
}
func TestSystemEnvironmentBuildUsesIsolatedSharedSlotAndDurableExport(t *testing.T) {
	testEnvironmentBuildUsesIsolatedSharedSlotAndDurableExport(t, api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, SystemPackages: []string{"poppler-utils=26.08.0-r0"}})
}
func testEnvironmentBuildUsesIsolatedSharedSlotAndDurableExport(t *testing.T, recipe api.CreateExecutionEnvironmentRequest) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "environment-builder@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	base := "registry.example/python@sha256:" + strings.Repeat("b", 64)
	digest, err := runenv.Digest(recipe, base)
	if err != nil {
		t.Fatal(err)
	}
	env, err := store.CreateExecutionEnvironment(ctx, state.ExecutionEnvironment{ID: uuid.NewString(), AccountID: acct.ID, Runtime: recipe.Runtime, Recipe: recipe, RecipeDigest: digest, BaseRef: base})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vm := &environmentVM{t: t, root: t.TempDir()}
	b := builderd.New(store, nil, vm, builderd.NewCache(t.TempDir()), builderd.NewDetector(), nil, builderd.Config{SourceSpoolDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithExecutionEnvironments(true, backend)
	result, err := b.ProcessNext(ctx)
	if err != nil || result.BuildID != env.ID || !vm.waited {
		t.Fatalf("build result %+v %v", result, err)
	}
	row, err := store.ExecutionEnvironmentByID(ctx, acct.ID, env.ID)
	if err != nil || runenv.PublicStatus(row.Status) != "built" || row.ExportDigest == "" {
		t.Fatalf("durable handoff %+v %v", row, err)
	}
	object, err := backend.Get(ctx, row.ExportKey)
	if err != nil {
		t.Fatal(err)
	}
	defer object.Close()
	body, _ := io.ReadAll(object)
	if string(body) != "builder-export" {
		t.Fatal("wrong export")
	}
	if _, err := os.Stat(vm.request.SourcePath); !os.IsNotExist(err) {
		t.Fatal("build source leaked", err)
	}
	if _, err := os.Stat(filepath.Join(vm.root, vm.request.BuildID)); !os.IsNotExist(err) {
		t.Fatal("local export leaked", err)
	}
}

type noisyEnvironmentVM struct {
	*environmentVM
	logsDone chan struct{}
}

func (v *noisyEnvironmentVM) StreamEnvironmentBuildLogs(ctx context.Context, _ builderd.BuildHandle, emit func(string, bool) error) error {
	defer close(v.logsDone)
	for i := 0; i < api.ExecutionEnvironmentDiagnosticLiveMaxEvents*2; i++ {
		if err := emit("#1 0.1 installing token=PRIVATE_TOKEN /srv/fc/PRIVATE_PATH", false); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return nil
}
func (v *noisyEnvironmentVM) WaitForCompletion(ctx context.Context, h builderd.BuildHandle) (builderd.BuildOutcome, error) {
	select {
	case <-v.logsDone:
	case <-ctx.Done():
		return builderd.BuildOutcome{}, ctx.Err()
	}
	return v.environmentVM.WaitForCompletion(ctx, h)
}
func TestEnvironmentLiveDiagnosticBudgetAndSanitization(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "noisy-builder@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	recipe := api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, Requirements: "six==1.17.0 --hash=sha256:" + strings.Repeat("a", 64)}
	base := "registry.example/python@sha256:" + strings.Repeat("b", 64)
	digest, err := runenv.Digest(recipe, base)
	if err != nil {
		t.Fatal(err)
	}
	env, err := store.CreateExecutionEnvironment(ctx, state.ExecutionEnvironment{ID: uuid.NewString(), AccountID: acct.ID, Runtime: recipe.Runtime, Recipe: recipe, RecipeDigest: digest, BaseRef: base})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vm := &noisyEnvironmentVM{environmentVM: &environmentVM{t: t, root: t.TempDir()}, logsDone: make(chan struct{})}
	b := builderd.New(store, nil, vm, builderd.NewCache(t.TempDir()), builderd.NewDetector(), nil, builderd.Config{SourceSpoolDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithExecutionEnvironments(true, backend)
	if _, err = b.ProcessNext(ctx); err != nil {
		t.Fatal(err)
	}
	row, err := store.ExecutionEnvironmentByID(ctx, acct.ID, env.ID)
	if err != nil || row.DiagnosticSequence > int64(api.ExecutionEnvironmentDiagnosticLiveMaxEvents+4) || len(row.Diagnostics) > api.ExecutionEnvironmentDiagnosticMaxEvents {
		t.Fatal("unbounded diagnostics", row.DiagnosticSequence, err)
	}
	gap := false
	for _, event := range row.Diagnostics {
		if event.Code == "log_gap" {
			gap = true
		}
		if strings.Contains(event.Message, "PRIVATE_") {
			t.Fatal("unsanitized transport output", event.Message)
		}
	}
	if !gap {
		t.Fatal("output budget did not produce a gap diagnostic")
	}
}
