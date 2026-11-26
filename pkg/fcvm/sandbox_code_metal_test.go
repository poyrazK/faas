//go:build metal

// adr: 20261027090013 step 14 — a session booted from the code-interpreter
// image runs code in a real Jupyter kernel: state between runs, streams,
// the run's value, rich output, errors, an interrupted run that keeps its
// state, and separate code contexts, all as the session's user.
package fcvm

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
	"github.com/onebox-faas/faas/pkg/wire"
)

// mainResultText is the text/plain of a run's value, or "" when it has none.
func mainResultText(t *testing.T, res sandboxproto.CodeRunResult) string {
	t.Helper()
	for _, out := range res.Results {
		if !out.IsMainResult {
			continue
		}
		var text string
		if err := json.Unmarshal(out.Data["text/plain"], &text); err != nil {
			t.Fatalf("text/plain of %v: %v", out.Data, err)
		}
		return text
	}
	return ""
}

func TestMetalSandboxCodeInterpreter(t *testing.T) {
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	base := os.Getenv("FAAS_TEST_AGENT_RUN_BASE")
	imageRoot := os.Getenv("FAAS_TEST_SANDBOX_CODE_IMAGE_ROOT")
	if kernel == "" || base == "" || imageRoot == "" {
		t.Skip("set FAAS_TEST_KERNEL, FAAS_TEST_AGENT_RUN_BASE and FAAS_TEST_SANDBOX_CODE_IMAGE_ROOT (the unpacked images/sandbox-code-interpreter.Dockerfile image)")
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	image := sellerImageDrive(t, imageRoot)
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 120*time.Second), Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), slog.Default(), nil)
	const instance, sandboxID = "sbx-code", "sbx-code-1"
	if _, err := m.WakeSandbox(context.Background(), SandboxWakeRequest{Instance: instance, SandboxID: sandboxID, AccountID: "acct-sandbox",
		Plan: api.PlanPro, BaseKey: base, LayerKey: sandboxRootLayer(t), ImageKey: image, VcpuCount: 2, MemSizeMiB: 1024,
		CPUMillicores: 2000, Network: api.SandboxNetworkNone}); err != nil {
		t.Fatalf("wake: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	run := func(t *testing.T, codeContext, code string, timeoutMillis int64) (string, sandboxproto.CodeRunResult) {
		t.Helper()
		var res sandboxproto.CodeRunResult
		frames, err := sandboxDo(t, m, instance, sandboxID, sandboxproto.Request{Op: sandboxproto.OpCodeRun, Context: codeContext,
			Code: code, TimeoutMillis: timeoutMillis, MaxOutputBytes: 1 << 20}, &res)
		if err != nil {
			t.Fatalf("code_run %q: %v", code, err)
		}
		return frames, res
	}
	contexts := func(t *testing.T, req sandboxproto.Request) sandboxproto.CodeContextResult {
		t.Helper()
		req.Op = sandboxproto.OpCodeContext
		var res sandboxproto.CodeContextResult
		if _, err := sandboxDo(t, m, instance, sandboxID, req, &res); err != nil {
			t.Fatalf("code_context %s: %v", req.Action, err)
		}
		return res
	}

	t.Run("keeps state, streams output and answers the value", func(t *testing.T) {
		started := time.Now()
		if _, res := run(t, "", "x = 41", 60_000); res.Error != nil {
			t.Fatalf("first run: %+v", res.Error)
		}
		t.Logf("first run, driver and kernel start included: %s", time.Since(started))
		started = time.Now()
		frames, res := run(t, "", "import os\nprint('hi', os.getuid())\nx + 1", 10_000)
		t.Logf("warm run: %s", time.Since(started))
		if !strings.Contains(frames, "hi 1000\n") {
			t.Errorf("stdout %q, want the session user's print", frames)
		}
		if got := mainResultText(t, res); got != "42" || res.ExecutionCount == nil {
			t.Errorf("value %q, execution count %v; want 42", got, res.ExecutionCount)
		}
	})

	t.Run("rich output", func(t *testing.T) {
		_, res := run(t, "", "import matplotlib.pyplot as plt\nplt.plot([1, 2, 3])\nplt.show()", 60_000)
		if res.Error != nil {
			t.Fatalf("plot: %+v", res.Error)
		}
		if !slices.ContainsFunc(res.Results, func(o sandboxproto.CodeOutput) bool { return len(o.Data["image/png"]) > 100 }) {
			t.Fatalf("no PNG among %d results", len(res.Results))
		}
	})

	t.Run("an error", func(t *testing.T) {
		_, res := run(t, "", "1/0", 10_000)
		if res.Error == nil || res.Error.Name != "ZeroDivisionError" || res.Error.Traceback == "" {
			t.Fatalf("error = %+v", res.Error)
		}
	})

	t.Run("a run past its timeout is interrupted and keeps the state", func(t *testing.T) {
		started := time.Now()
		_, res := run(t, "", "import time\ntime.sleep(60)", 1_000)
		if !res.TimedOut {
			t.Fatalf("not timed out: %+v", res)
		}
		if took := time.Since(started); took > 15*time.Second {
			t.Fatalf("the interrupted run took %s", took)
		}
		if _, res := run(t, "", "x", 10_000); mainResultText(t, res) != "41" {
			t.Fatalf("after the interrupt x = %q, want 41", mainResultText(t, res))
		}
	})

	t.Run("separate contexts", func(t *testing.T) {
		if out, code := userExec(t, m, instance, sandboxID, "mkdir -p /workspace/sub"); code != 0 {
			t.Fatalf("mkdir: exit %d %s", code, out)
		}
		created := contexts(t, sandboxproto.Request{Action: sandboxproto.CodeActionCreate, Language: "python", Workdir: "sub"})
		if created.Context == nil || created.Context.ID == "" || created.Context.Cwd != "/workspace/sub" {
			t.Fatalf("create = %+v", created.Context)
		}
		id := created.Context.ID
		if _, res := run(t, id, "x", 30_000); res.Error == nil || res.Error.Name != "NameError" {
			t.Fatalf("a new context sees the default one's x: %+v", res)
		}
		if _, res := run(t, id, "import os\nos.getcwd()", 10_000); mainResultText(t, res) != "'/workspace/sub'" {
			t.Fatalf("cwd = %q", mainResultText(t, res))
		}
		listed := contexts(t, sandboxproto.Request{Action: sandboxproto.CodeActionList})
		ids := []string{}
		for _, c := range listed.Contexts {
			ids = append(ids, c.ID)
		}
		if !slices.Contains(ids, "default") || !slices.Contains(ids, id) {
			t.Fatalf("contexts = %v", ids)
		}
		contexts(t, sandboxproto.Request{Action: sandboxproto.CodeActionRemove, Context: id})
		var res sandboxproto.CodeRunResult
		if _, err := sandboxDo(t, m, instance, sandboxID, sandboxproto.Request{Op: sandboxproto.OpCodeRun, Context: id,
			Code: "1", TimeoutMillis: 10_000, MaxOutputBytes: 1 << 16}, &res); err == nil {
			t.Fatal("a removed context still runs code")
		}
	})
}
