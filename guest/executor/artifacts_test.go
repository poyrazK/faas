package executor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
	"golang.org/x/sys/unix"
)

func TestExecutionExportsFilesAndDiscardsScratch(t *testing.T) {
	cases := []struct {
		name, interpreter string
		runtime           api.ExecutionRuntime
		source            string
	}{
		{"node", "node", api.ExecutionRuntimeNode22, `import fs from 'node:fs'; export default (_, context) => { fs.writeFileSync(context.output_dir+'/patch.diff', 'patch'); fs.writeFileSync(context.output_dir+'/data.csv', 'x,y\n1,2\n'); return {directory: context.output_dir}; }`},
		{"python", "python3", api.ExecutionRuntimePython313, "import os\ndef main(input, context):\n    open(os.path.join(context['output_dir'], 'patch.diff'), 'w').write('patch')\n    open(os.path.join(context['output_dir'], 'data.csv'), 'w').write('x,y\\n1,2\\n')\n    return {'directory': context['output_dir']}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.interpreter); err != nil {
				t.Skip(tc.interpreter + " unavailable")
			}
			req := executionproto.Request{Version: executionproto.ArtifactVersion, ExecutionID: "exports", Runtime: tc.runtime, Source: tc.source, Input: json.RawMessage("null"), TimeoutMS: nodeExecutionTestTimeoutMS, MaxOutput: 4096, NetworkMode: api.ExecutionNetworkNone, OutputFiles: []string{"patch.diff", "data.csv"}}
			result, err := New().Handle(context.Background(), req, nil, nil)
			if err != nil || result.Status != api.ExecutionStatusSucceeded {
				t.Fatalf("Handle = %+v, %v", result, err)
			}
			if !api.ExecutionArtifactsMatch(req.OutputFiles, result.Artifacts) || string(result.Artifacts[0].Content) != "patch" || string(result.Artifacts[1].Content) != "x,y\n1,2\n" {
				t.Fatalf("artifacts = %+v", result.Artifacts)
			}
			if err := result.Validate(req.MaxOutput); err != nil {
				t.Fatal(err)
			}
			var value struct {
				Directory string `json:"directory"`
			}
			if err := json.Unmarshal(result.Result, &value); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(value.Directory); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("scratch retained: %v", err)
			}
			// A second run cannot recover the first one's file through output selection.
			if tc.name == "node" {
				req.Source = `export default () => null`
			} else {
				req.Source = "def main(input, context):\n    return None\n"
			}
			second, err := New().Handle(context.Background(), req, nil, nil)
			if err != nil || second.FailureCode != "artifact_invalid" || len(second.Artifacts) != 0 {
				t.Fatalf("next run = %+v, %v", second, err)
			}
		})
	}
}

func TestCollectArtifactsRejectsLinksSpecialFilesAndEncodedOverflow(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "parent")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(dir, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"link", "parent/secret", "fifo", "folder", "missing"} {
		if _, err := collectArtifacts(context.Background(), dir, []string{name}, 1024); err == nil {
			t.Fatalf("exported %s", name)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), make([]byte, 800), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := collectArtifacts(context.Background(), dir, []string{"file"}, 1024); !errors.Is(err, executionproto.ErrOutputLimitExceeded) {
		t.Fatalf("base64/metadata budget: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := collectArtifacts(ctx, dir, []string{"file"}, 4096); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestExecutionArtifactFailuresDiscardAllOutputs(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	for _, tc := range []struct{ name, source, code string }{
		{"partial", `import fs from 'node:fs'; export default (_, c) => { fs.writeFileSync(c.output_dir+'/first', 'ok'); return null; }`, "artifact_invalid"},
		{"encoded_overflow", `import fs from 'node:fs'; export default (_, c) => { fs.writeFileSync(c.output_dir+'/first', Buffer.alloc(800)); fs.writeFileSync(c.output_dir+'/second', 'ok'); return null; }`, "output_limit"},
		{"interpreter_failure", `import fs from 'node:fs'; export default (_, c) => { fs.writeFileSync(c.output_dir+'/first', 'ok'); throw Error('failed'); }`, "guest_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := executionproto.Request{Version: executionproto.ArtifactVersion, ExecutionID: "fail-export", Runtime: api.ExecutionRuntimeNode22, Source: tc.source, Input: json.RawMessage("null"), TimeoutMS: nodeExecutionTestTimeoutMS, MaxOutput: 1024, NetworkMode: api.ExecutionNetworkNone, OutputFiles: []string{"first", "second"}}
			result, err := New().Handle(context.Background(), req, nil, nil)
			if err != nil || result.FailureCode != tc.code || len(result.Artifacts) != 0 {
				t.Fatalf("failure = %+v, %v", result, err)
			}
		})
	}
}

func TestExecutionBundleCannotPopulateOutputDirectory(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	req := executionproto.Request{Version: executionproto.ArtifactVersion, ExecutionID: "separate-outputs", Runtime: api.ExecutionRuntimeNode22, Entrypoint: "main.mjs", Files: []api.ExecutionFile{{Path: "main.mjs", Content: []byte("export default () => null")}, {Path: "outputs/staged", Content: []byte("source")}}, Input: json.RawMessage("null"), TimeoutMS: nodeExecutionTestTimeoutMS, MaxOutput: 1024, NetworkMode: api.ExecutionNetworkNone, OutputFiles: []string{"staged"}}
	result, err := New().Handle(context.Background(), req, nil, nil)
	if err != nil || result.FailureCode != "artifact_invalid" || len(result.Artifacts) != 0 {
		t.Fatalf("source exported as output: %+v, %v", result, err)
	}
}
