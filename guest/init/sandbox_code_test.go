package main

// adr: 20261027090013 step 14 — guest-init starts the code interpreter's
// driver on first use and relays runs and contexts to it: a run's stdout
// streams, its value and error come back in the result, and state persists
// between runs. A sandbox without the driver answers unsupported.

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

func codeRunReq(code string) sandboxproto.Request {
	return sandboxproto.Request{Op: sandboxproto.OpCodeRun, Code: code, TimeoutMillis: 30_000, MaxOutputBytes: 1 << 20}
}

func TestSandboxCodeWithoutAnInterpreter(t *testing.T) {
	f := newSandboxFixture(t)
	f.srv.code.path = filepath.Join(t.TempDir(), "absent.py")
	if r := f.call(t, codeRunReq("1"), nil, nil); r.code() != sandboxproto.CodeUnsupported {
		t.Fatalf("a run without a driver: %v", r.err)
	}
}

func TestSandboxCodeRuns(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil || exec.Command(python, "-c", "import ipykernel, jupyter_client").Run() != nil {
		t.Skip("needs python3 with ipykernel and jupyter_client on PATH")
	}
	driver, err := filepath.Abs("../../images/sandbox-code-interpreter/driver.py")
	if err != nil {
		t.Fatal(err)
	}
	f := newSandboxFixture(t)
	f.srv.code.path, f.srv.code.python = driver, python
	f.srv.code.runtimeDir = filepath.Join(t.TempDir(), "run")
	t.Cleanup(func() {
		if f.srv.code.proc != nil {
			_ = f.srv.code.proc.signal(9)
		}
	})

	f.call(t, codeRunReq("x = 40"), nil, nil).decode(t, &sandboxproto.CodeRunResult{})
	r := f.call(t, codeRunReq("print('hi')\nx + 2"), nil, nil)
	var res sandboxproto.CodeRunResult
	r.decode(t, &res)
	if r.stdout.String() != "hi\n" || len(res.Results) != 1 || !res.Results[0].IsMainResult ||
		string(res.Results[0].Data["text/plain"]) != `"42"` || res.ExecutionCount == nil {
		t.Fatalf("stdout %q, result %+v", r.stdout.String(), res)
	}
	f.call(t, codeRunReq("1/0"), nil, nil).decode(t, &res)
	if res.Error == nil || res.Error.Name != "ZeroDivisionError" {
		t.Fatalf("an error run: %+v", res)
	}

	var created sandboxproto.CodeContextResult
	f.call(t, sandboxproto.Request{Op: sandboxproto.OpCodeContext, Action: sandboxproto.CodeActionCreate}, nil, nil).decode(t, &created)
	if created.Context == nil || created.Context.Cwd != f.workspace || created.Context.Language != "python" {
		t.Fatalf("created %+v", created)
	}
	other := codeRunReq("'x' in dir()")
	other.Context = created.Context.ID
	f.call(t, other, nil, nil).decode(t, &res)
	if string(res.Results[0].Data["text/plain"]) != `"False"` {
		t.Fatalf("a new context saw the default one's state: %+v", res)
	}
	var listed sandboxproto.CodeContextResult
	f.call(t, sandboxproto.Request{Op: sandboxproto.OpCodeContext, Action: sandboxproto.CodeActionList}, nil, nil).decode(t, &listed)
	raw, _ := json.Marshal(listed)
	if !strings.Contains(string(raw), created.Context.ID) || !strings.Contains(string(raw), `"default"`) {
		t.Fatalf("listed %s", raw)
	}

	// The session's environment set after the first run reaches the running
	// kernel, and clearing it takes the variable away again.
	f.srv.setSessionEnv(map[string]string{"GREGALE_TEST_TOKEN": "t0k"})
	f.call(t, codeRunReq("import os\nos.environ.get('GREGALE_TEST_TOKEN')"), nil, nil).decode(t, &res)
	if string(res.Results[0].Data["text/plain"]) != `"'t0k'"` {
		t.Fatalf("the kernel did not see the session env set after its start: %+v", res)
	}
	f.srv.setSessionEnv(map[string]string{})
	f.call(t, codeRunReq("import os\nos.environ.get('GREGALE_TEST_TOKEN')"), nil, nil).decode(t, &res)
	if len(res.Results) != 0 {
		t.Fatalf("a cleared variable stayed in the kernel: %+v", res)
	}

	// Rich results past one control frame, as this guest encodes them (HTML
	// escapes grow), are dropped and reported rather than failing the run.
	big := codeRunReq("from IPython.display import display, HTML\nfor _ in range(6): display(HTML('<b>' * 120_000))")
	big.MaxOutputBytes = sandboxproto.MaxOutputBytes
	f.call(t, big, nil, nil).decode(t, &res)
	if !res.OutputTruncated || len(res.Results) == 0 || len(res.Results) >= 6 {
		t.Fatalf("big results: %d kept, truncated %v", len(res.Results), res.OutputTruncated)
	}

	gone := sandboxproto.Request{Op: sandboxproto.OpCodeContext, Action: sandboxproto.CodeActionRemove, Context: created.Context.ID}
	f.call(t, gone, nil, nil).decode(t, &sandboxproto.CodeContextResult{})
	if r := f.call(t, other, nil, nil); r.code() != sandboxproto.CodeNotFound {
		t.Fatalf("a run in a removed context: %v", r.err)
	}
}
