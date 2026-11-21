//go:build metal

package runlifecycle_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderd"
	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/runenv"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A separate cohort keeps language-only recipes on their qualified installer
// bases. This fixture builds the selected Wolfi packages in a real builder VM.
func TestSourceSystemEnvironmentMetal(t *testing.T) {
	if !runenv.ValidBaseRef(os.Getenv("FAAS_TEST_SYSTEM_ENVIRONMENT_BASE_REF")) {
		t.Fatal("a digest-pinned Wolfi installer base is required")
	}
	pins := strings.Split(os.Getenv("FAAS_TEST_SYSTEM_ENVIRONMENT_PACKAGES"), ",")
	recipe := api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, SystemPackages: pins}
	if err := runenv.Validate(recipe); err != nil {
		t.Fatal("exact Python and Poppler package pins are required", err)
	}
	t.Setenv("FAAS_RUN_ENVIRONMENT_QUALIFICATION", "1")
	t.Setenv("FAAS_RUN_TASK_QUALIFICATION", "1")
	t.Setenv("FAAS_RUN_FILE_QUALIFICATION", "1")
	f := newRunFixture(t)
	store := state.NewPgStore(f.h.Pool)
	backend, err := storage.NewLocalStorageBackend(f.storageRoot)
	if err != nil {
		t.Fatal(err)
	}
	var environment api.ExecutionEnvironmentResponse
	if !t.Run("versioned_system_recipe_and_immutable_reuse", func(t *testing.T) {
		environment, err = f.client.CreateExecutionEnvironment(t.Context(), recipe)
		if err != nil || environment.Status != "queued" {
			t.Fatal(environment, err)
		}
		row, e := store.ExecutionEnvironmentByID(t.Context(), f.accountID, environment.ID)
		if e != nil || row.Status != "queued_system_v1" {
			t.Fatal("legacy worker could claim the new recipe", row, e)
		}
		again, e := f.client.CreateExecutionEnvironment(t.Context(), recipe)
		if e != nil || again.ID != environment.ID {
			t.Fatal("recipe replay changed identity", e)
		}
	}) {
		return
	}
	handler := imaged.New(store, nil, nil, rootfs.NewBuilder(wire.ExecRunner{}), os.Getenv("FAAS_GUEST_INIT"), filepath.Join(f.root, "apps"), slog.Default()).WithStorage(backend).WithExecutionEnvironments(true)
	if !t.Run("isolated_system_install_and_scanned_publication", func(t *testing.T) {
		driver, e := builderd.NewVMMDriverContext(t.Context(), f.h.VMMDSock, nil, os.Getenv("FAAS_TEST_ENVIRONMENT_BUILDER_BASE"), filepath.Join(f.root, "system-build-drives"), filepath.Join(f.root, "system-build-exports"))
		if e != nil {
			t.Fatal(e)
		}
		defer driver.Close()
		worker := builderd.New(store, nil, &environmentAcceptanceVM{driver, t}, builderd.NewCache(filepath.Join(f.root, "system-build-cache")), builderd.NewDetector(), nil, builderd.Config{SourceSpoolDir: filepath.Join(f.root, "system-source-spool"), DisableWarmBuilders: true}, slog.Default()).WithExecutionEnvironments(true, backend)
		requireLiveEnvironmentBuild(t, f, environment.ID, worker)
		if e = handler.MaterializeExecutionEnvironments(t.Context()); e != nil {
			t.Fatal(e)
		}
		environment, e = f.client.GetExecutionEnvironment(t.Context(), environment.ID)
		if e != nil || environment.Status != "ready" {
			t.Fatal(environment, e)
		}
		row, e := store.ExecutionEnvironmentByID(t.Context(), f.accountID, environment.ID)
		if e != nil {
			t.Fatal(e)
		}
		producer, e := store.GetCurrentBaseImageProducer(t.Context(), row.BaseKey)
		if e != nil || len(producer.Input.Layers) == 0 {
			t.Fatal("missing exact consumed-layer proof", e)
		}
		scan, e := store.GetCurrentBaseImageScan(t.Context(), row.BaseKey)
		if e != nil || scan.Input.Status != "complete" {
			t.Fatal("missing real vulnerability verdict", e)
		}
		t.Logf("system environment recipe=%s image=%s bytes=%d", environment.RecipeDigest, environment.ImageDigest, environment.ArtifactBytes)
	}) {
		return
	}
	if !t.Run("pdf_task_stored_report_contract_and_replay", func(t *testing.T) {
		example := filepath.Join("..", "..", "..", "examples", "gregale-run", "pdf-report")
		source, e := os.ReadFile(filepath.Join(example, "handler.py"))
		if e != nil {
			t.Fatal(e)
		}
		sample := filepath.Join(t.TempDir(), "input.pdf")
		if output, e := exec.CommandContext(t.Context(), "python3", filepath.Join(example, "make_sample.py"), sample).CombinedOutput(); e != nil {
			t.Fatal(e, string(output))
		}
		data, e := os.ReadFile(sample)
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(data)
		digest := "sha256:" + hex.EncodeToString(sum[:])
		input, e := f.client.PrepareExecutionFile(t.Context(), api.CreateExecutionFileRequest{SizeBytes: int64(len(data)), SHA256: digest})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.client.UploadExecutionFile(t.Context(), input.ID, bytes.NewReader(data), int64(len(data)), digest); e != nil {
			t.Fatal(e)
		}
		definition := runRequest(string(source))
		definition.Runtime = api.ExecutionRuntimePython313
		definition.EnvironmentID = environment.ID
		definition.Input = nil
		definition.Limits.MemoryMB = 512
		definition.Limits.TimeoutMS = 30000
		definition.OutputFiles = []string{"report.html"}
		definition.ArtifactStorage = api.ExecutionArtifactStored
		definition.Limits.MaxArtifactBytes = 262144
		definition.InputSchema = json.RawMessage(`{"type":"object","required":["title"],"properties":{"title":{"type":"string"}}}`)
		definition.ResultSchema = json.RawMessage(`{"type":"object","required":["pages","words","preview_truncated"],"properties":{"pages":{"const":1},"words":{"const":5},"preview_truncated":{"const":false}}}`)
		if _, e = f.client.PublishExecutionTask(t.Context(), api.PublishExecutionTaskRequest{Name: "pdf-report", Version: 1, Definition: definition}); e != nil {
			t.Fatal(e)
		}
		request := api.InvokeExecutionTaskRequest{Name: "pdf-report", Version: 1, Input: json.RawMessage(`{"title":"<Customer report>"}`), InputFiles: []api.ExecutionFileInput{{Path: "input.pdf", FileID: input.ID}}}
		admitted, e := f.client.InvokeExecutionTask(t.Context(), request, "system-pdf-report")
		if e != nil {
			t.Fatal(e)
		}
		f.runs[admitted.ID] = true
		result := f.wait(t, admitted.ID, api.ExecutionStatusSucceeded)
		if result.ResultValidation == nil || result.ResultValidation.Status != "valid" || result.EnvironmentID != environment.ID || len(result.Artifacts) != 1 || result.Artifacts[0].FileID == "" {
			t.Fatal("invalid report receipt", result)
		}
		var html bytes.Buffer
		if _, e = f.client.DownloadExecutionFile(t.Context(), result.Artifacts[0].FileID, io.Writer(&html)); e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(html.String(), "Customer report from Gregale Run.") || !strings.Contains(html.String(), "&lt;Customer report&gt;") || strings.Contains(html.String(), "<Customer report>") {
			t.Fatal("wrong or unescaped PDF report")
		}
		replay, e := f.client.InvokeExecutionTask(t.Context(), request, "system-pdf-report")
		if e != nil || replay.ID != admitted.ID {
			t.Fatal("task replay reran PDF conversion", e)
		}
	}) {
		return
	}
	if !t.Run("retirement_and_single_usage_cleanup", func(t *testing.T) {
		f.assertSettled(t)
		if e := f.client.DeleteExecutionEnvironment(t.Context(), environment.ID); e != nil {
			t.Fatal(e)
		}
		if e := handler.MaterializeExecutionEnvironments(t.Context()); e != nil {
			t.Fatal(e)
		}
		if _, e := store.ExecutionEnvironmentByID(t.Context(), f.accountID, environment.ID); !errors.Is(e, state.ErrNotFound) {
			t.Fatal("retired environment was not collected", e)
		}
	}) {
		return
	}
}
