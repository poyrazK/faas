//go:build metal

package runlifecycle_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderd"
	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/runenv"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

const environmentNodeBase = "mirror.gcr.io/library/node:24-trixie-slim@sha256:60587baa7c895cf438d44f691e4ac8248b36c13b26be77332f874bebb3a64201"

const environmentPythonBase = "mirror.gcr.io/library/python:3.13-slim@sha256:e48f59cdba69a3cbd9ed2cf0f348d90d45a8d350d98134cf1070bc106ee9ed90"

func TestSourceExtendedEnvironmentMetal(t *testing.T) {
	t.Setenv("FAAS_RUN_ENVIRONMENT_QUALIFICATION", "1")
	f := newRunFixture(t)
	store := state.NewPgStore(f.h.Pool)
	completion := newEnvironmentCompletionReceiver(t, f, store)
	backend, err := storage.NewLocalStorageBackend(f.storageRoot)
	if err != nil {
		t.Fatal(err)
	}
	var env api.ExecutionEnvironmentResponse
	recipe := api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimeNode24, PackageJSON: `{"name":"gregale-run-env-test","version":"1.0.0","dependencies":{"is-number":"7.0.0"}}`, PackageLock: string(mustReadEnvironmentFixture(t))}
	if !t.Run("unready_environment_and_recipe_reuse", func(t *testing.T) {
		env, err = f.client.CreateExecutionEnvironment(t.Context(), recipe)
		if err != nil || env.Status != "queued" {
			t.Fatalf("create: %+v %v", env, err)
		}
		token, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.CreateAPIKey(t.Context(), f.accountID, hash, "other-environment-family", []string{api.ScopeRunsRead, api.ScopeRunsWrite}); err != nil {
			t.Fatal(err)
		}
		other := api.NewClient(f.h.APIDURL, token)
		_, err = other.GetExecutionEnvironment(t.Context(), env.ID)
		var forbidden *api.APIError
		if !errors.As(err, &forbidden) || forbidden.Problem.Status != 404 {
			t.Fatal("another key family read environment", err)
		}
		again, err := f.client.CreateExecutionEnvironment(t.Context(), recipe)
		if err != nil || again.ID != env.ID {
			t.Fatal("recipe cache", err)
		}
		req := runRequest(`export default () => 1`)
		req.EnvironmentID = env.ID
		_, err = f.client.CreateExecution(t.Context(), req)
		var p *api.APIError
		if !errors.As(err, &p) || p.Problem.Status != 404 {
			t.Fatal("unready environment admitted", err)
		}
	}) {
		return
	}
	if !t.Run("isolated_builder_and_provenance_publication", func(t *testing.T) {
		driver, err := builderd.NewVMMDriverContext(t.Context(), f.h.VMMDSock, nil, os.Getenv("FAAS_TEST_ENVIRONMENT_BUILDER_BASE"), filepath.Join(f.root, "build-drives"), filepath.Join(f.root, "build-exports"))
		if err != nil {
			t.Fatal(err)
		}
		defer driver.Close()
		worker := builderd.New(store, nil, &environmentAcceptanceVM{driver, t}, builderd.NewCache(filepath.Join(f.root, "build-cache")), builderd.NewDetector(), nil, builderd.Config{SourceSpoolDir: filepath.Join(f.root, "source-spool"), DisableWarmBuilders: true}, slog.Default()).WithExecutionEnvironments(true, backend)
		requireLiveEnvironmentBuild(t, f, env.ID, worker)
		handler := imaged.New(store, nil, nil, rootfs.NewBuilder(wire.ExecRunner{}), os.Getenv("FAAS_GUEST_INIT"), filepath.Join(f.root, "apps"), slog.Default()).WithStorage(backend).WithExecutionEnvironments(true)
		if err = handler.MaterializeExecutionEnvironments(t.Context()); err != nil {
			t.Fatal(err)
		}
		env, err = f.client.GetExecutionEnvironment(t.Context(), env.ID)
		if err != nil || env.Status != "ready" || env.ImageDigest == "" {
			t.Fatalf("publication: %+v %v", env, err)
		}
		row, err := store.ExecutionEnvironmentByID(t.Context(), f.accountID, env.ID)
		if err != nil {
			t.Fatal(err)
		}
		producer, err := store.GetCurrentBaseImageProducer(t.Context(), row.BaseKey)
		if err != nil || len(producer.Input.Layers) == 0 {
			t.Fatal("missing consumed OCI proof", err)
		}
		scan, err := store.GetCurrentBaseImageScan(t.Context(), row.BaseKey)
		if err != nil || scan.Input.Status != "complete" {
			t.Fatal("missing exact-byte scan", err)
		}
		t.Logf("environment publication: recipe=%s image=%s bytes=%d scanner=grype", env.RecipeDigest, env.ImageDigest, env.ArtifactBytes)
	}) {
		return
	}
	if !t.Run("python_hash_pinned_wheel_build_and_import", func(t *testing.T) {
		pythonRecipe := api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, Requirements: "six==1.17.0 --hash=sha256:4721f391ed90541fddacab5acf947aa0d3dc7d27b2e1e8eda2be8970586c3274\n"}
		pythonEnv, err := f.client.CreateExecutionEnvironment(t.Context(), pythonRecipe)
		if err != nil {
			t.Fatal(err)
		}
		driver, err := builderd.NewVMMDriverContext(t.Context(), f.h.VMMDSock, nil, os.Getenv("FAAS_TEST_ENVIRONMENT_BUILDER_BASE"), filepath.Join(f.root, "python-build-drives"), filepath.Join(f.root, "python-build-exports"))
		if err != nil {
			t.Fatal(err)
		}
		defer driver.Close()
		worker := builderd.New(store, nil, &environmentAcceptanceVM{driver, t}, builderd.NewCache(filepath.Join(f.root, "python-build-cache")), builderd.NewDetector(), nil, builderd.Config{SourceSpoolDir: filepath.Join(f.root, "python-source-spool"), DisableWarmBuilders: true}, slog.Default()).WithExecutionEnvironments(true, backend)
		if _, err = worker.ProcessNext(t.Context()); err != nil {
			t.Fatal(err)
		}
		handler := imaged.New(store, nil, nil, rootfs.NewBuilder(wire.ExecRunner{}), os.Getenv("FAAS_GUEST_INIT"), filepath.Join(f.root, "apps"), slog.Default()).WithStorage(backend).WithExecutionEnvironments(true)
		if err = handler.MaterializeExecutionEnvironments(t.Context()); err != nil {
			t.Fatal(err)
		}
		req := runRequest("import six, os\ndef main(input, context):\n    return {\"version\":six.__version__,\"uid\":os.getuid()}\n")
		req.Runtime = api.ExecutionRuntimePython313
		req.EnvironmentID = pythonEnv.ID
		row := f.wait(t, f.submit(t, "python-environment-import", req).ID, api.ExecutionStatusSucceeded)
		ready, err := f.client.GetExecutionEnvironment(t.Context(), pythonEnv.ID)
		if err != nil || row.EnvironmentID != ready.ID || row.RuntimeImageDigest != "sha256:"+ready.ImageDigest {
			t.Fatal("Python runtime selection lost", err)
		}
		t.Logf("Python environment publication: recipe=%s image=%s bytes=%d scanner=grype", ready.RecipeDigest, ready.ImageDigest, ready.ArtifactBytes)

		var result struct {
			Version string
			UID     int
		}
		if json.Unmarshal(row.Result, &result) != nil || result.Version != "1.17.0" || result.UID != 1000 {
			t.Fatalf("python dependency result %s", row.Result)
		}
		if err = f.client.DeleteExecutionEnvironment(t.Context(), pythonEnv.ID); err != nil {
			t.Fatal(err)
		}
		if err = handler.MaterializeExecutionEnvironments(t.Context()); err != nil {
			t.Fatal(err)
		}
	}) {
		return
	}
	importSource := `import isNumber from 'is-number'; import fs from 'node:fs'; export default async (_,c)=>{ const fresh=!fs.existsSync('/tmp/previous-env-run');fs.writeFileSync('/tmp/previous-env-run','private');fs.writeFileSync(c.output_dir+'/answer.txt','42');return {ok:isNumber('42'),fresh,uid:process.getuid()}; }`
	if !t.Run("dependency_import_and_fresh_vm_isolation", func(t *testing.T) {
		for _, key := range []string{"environment-import-a", "environment-import-b"} {
			req := runRequest(importSource)
			req.EnvironmentID = env.ID
			req.OutputFiles = []string{"answer.txt"}
			req.ResultSchema = json.RawMessage(`{"type":"object","properties":{"ok":{"const":true},"fresh":{"const":true},"uid":{"const":1000}},"required":["ok","fresh","uid"]}`)
			row := f.wait(t, f.submit(t, key, req).ID, api.ExecutionStatusSucceeded)
			if row.EnvironmentID != env.ID || row.ResultValidation == nil || row.ResultValidation.Status != "valid" || len(row.Artifacts) != 1 || string(row.Artifacts[0].Content) != "42" {
				t.Fatalf("bad environment result: %+v", row)
			}
		}
	}) {
		return
	}
	if !t.Run("long_run_does_not_block_standard_lane", func(t *testing.T) {
		req := runRequest(`export default async ()=>{console.log('running-marker');await new Promise(r=>setTimeout(r,45000));return 42;}`)
		req.EnvironmentID = env.ID
		req.Limits.Class = api.ExecutionClassExtended
		req.Limits.TimeoutMS = 300000
		long := f.submit(t, "environment-extended", req)
		f.waitGuestStarted(t, long.ID)
		short := f.wait(t, f.submit(t, "standard-during-extended", runRequest(`export default ()=>42`)).ID, api.ExecutionStatusSucceeded)
		live, err := f.client.GetExecution(t.Context(), long.ID)
		if err != nil || live.Status != api.ExecutionStatusRunning || short.Usage == nil {
			t.Fatal("standard lane failed", err)
		}
		completed := f.wait(t, long.ID, api.ExecutionStatusSucceeded)
		if completed.Usage == nil || completed.Usage.WallTimeMS < 40000 {
			t.Fatal("extended Run stopped at standard limit")
		}
	}) {
		return
	}
	if !t.Run("five_minute_deadline_and_cancel_release_capacity", func(t *testing.T) {
		req := runRequest(`export default async ()=>{console.log('running-marker');await new Promise(r=>setTimeout(r,310000));return 42;}`)
		req.Limits.Class = api.ExecutionClassExtended
		req.Limits.TimeoutMS = 300000
		req.EnvironmentID = env.ID
		started := time.Now()
		run := f.submit(t, "environment-five-minute-timeout", req)
		f.waitGuestStarted(t, run.ID)
		deadline := time.After(330 * time.Second)
		for {
			row, err := f.client.GetExecution(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if row.Status.Terminal() {
				if row.Status != api.ExecutionStatusTimedOut || time.Since(started) < 290*time.Second {
					t.Fatalf("five-minute deadline not enforced: %s after %s", row.Status, time.Since(started))
				}
				f.waitRetired(t, run.ID)
				break
			}
			select {
			case <-deadline:
				t.Fatal("five-minute Run did not time out")
			case <-time.After(time.Second):
			}
		}
		cancelled := f.submit(t, "environment-extended-cancel", req)
		f.waitGuestStarted(t, cancelled.ID)
		if _, err = f.client.CancelExecution(t.Context(), cancelled.ID); err != nil {
			t.Fatal(err)
		}
		f.wait(t, cancelled.ID, api.ExecutionStatusCancelled)
	}) {
		return
	}
	if !t.Run("retirement_preserves_admitted_run_then_collects", func(t *testing.T) {
		req := runRequest(`import isNumber from 'is-number';export default async ()=>{console.log('running-marker');await new Promise(r=>setTimeout(r,5000));return isNumber('42');}`)
		req.EnvironmentID = env.ID
		active := f.submit(t, "environment-retiring", req)
		completion.hold(active.ID)
		f.waitGuestStarted(t, active.ID)
		if err = f.client.DeleteExecutionEnvironment(t.Context(), env.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = f.client.CreateExecution(t.Context(), req); err == nil {
			t.Fatal("retired environment admitted new Run")
		}
		handler := imaged.New(store, nil, nil, rootfs.NewBuilder(wire.ExecRunner{}), os.Getenv("FAAS_GUEST_INIT"), filepath.Join(f.root, "apps"), slog.Default()).WithStorage(backend)
		if err = handler.MaterializeExecutionEnvironments(t.Context()); err != nil {
			t.Fatal(err)
		}
		f.wait(t, active.ID, api.ExecutionStatusSucceeded)
		row, err := store.ExecutionEnvironmentByID(t.Context(), f.accountID, env.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = handler.MaterializeExecutionEnvironments(t.Context()); err != nil {
			t.Fatal(err)
		}
		for _, key := range row.ArtifactKeys {
			exists, err := backend.Exists(t.Context(), key)
			if err != nil || exists {
				t.Fatal("environment artifact retained", key, err)
			}
		}
		if _, err = store.ExecutionEnvironmentByID(t.Context(), f.accountID, env.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("retired row not collected", err)
		}
	}) {
		return
	}
	if !t.Run("completion_deliveries_survive_scheduler_restart", func(t *testing.T) { completion.qualify(t, f, store) }) {
		return
	}
	t.Run("payload_erasure_single_usage_and_no_live_resources", f.assertSettled)
}

func mustReadEnvironmentFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/environment-package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// Record only this public-dependency fixture's bounded diagnostics. Keeping the
// console open across Destroy preserves the boot failure even after teardown.
type environmentAcceptanceVM struct {
	*builderd.VMMDriver
	t *testing.T
}

func (v *environmentAcceptanceVM) WaitForCompletion(ctx context.Context, h builderd.BuildHandle) (builderd.BuildOutcome, error) {
	var console *os.File
	if id := strings.TrimPrefix(h.Instance, "build-"); runenv.ValidID(id) && h.Instance == "build-"+id {
		console, _ = os.Open(filepath.Join("/var/log/faas", "vm-"+h.Instance+".console")) //nolint:forbidigo // Root-owned console for this fixture's validated build UUID.
	}
	if console != nil {
		defer console.Close()
	}
	outcome, err := v.VMMDriver.WaitForCompletion(ctx, h)
	if err != nil || outcome.ExitCode != 0 {
		tail := outcome.LogTail
		if len(tail) > 8192 {
			tail = tail[len(tail)-8192:]
		}
		v.t.Logf("fixture builder: exit=%d failure_class=%s failure_code=%s log_tail=%s error=%v", outcome.ExitCode, outcome.FailureClass, outcome.FailureCode, tail, err)
		if console != nil {
			if stat, e := console.Stat(); e == nil {
				_, _ = console.Seek(max(0, stat.Size()-8192), io.SeekStart)
				data, _ := io.ReadAll(io.LimitReader(console, 8192))
				v.t.Logf("fixture builder console: %s", data)
			}
		}
	}
	return outcome, err
}
