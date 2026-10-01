package executor

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

func dataProfileRequest() executionproto.Request {
	return executionproto.Request{Version: executionproto.ProfileVersion, Runtime: api.ExecutionRuntimePython313, Profile: api.ExecutionProfilePythonDataV1, ExecutionID: "data-profile", Source: "def main(input, context): return input", Input: json.RawMessage("null"), TimeoutMS: 5000, MaxOutput: 4096, NetworkMode: api.ExecutionNetworkNone}
}

func TestExecutionRejectsProfileGuestMismatchBeforeDispatch(t *testing.T) {
	if _, err := New().Handle(context.Background(), dataProfileRequest(), nil, nil); err == nil {
		t.Fatal("data request accepted by standard image")
	}
	req := dataProfileRequest()
	req.Profile = api.ExecutionProfileStandard
	if _, err := NewWithProfile(api.ExecutionProfilePythonDataV1).Handle(context.Background(), req, nil, nil); err == nil {
		t.Fatal("standard request accepted by data image")
	}
	req = dataProfileRequest()
	req.Version = executionproto.ArtifactVersion
	if err := req.Validate(); err == nil {
		t.Fatal("data profile accepted on old guest protocol")
	}
}

func TestPythonDataProfileAnalyzesAndExportsCSV(t *testing.T) {
	interpreter := os.Getenv("FAAS_TEST_PYTHON_DATA")
	if interpreter == "" {
		t.Skip("set FAAS_TEST_PYTHON_DATA to a Python 3.13 environment with the pinned profile packages")
	}
	t.Setenv("PATH", filepath.Dir(interpreter)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OPENBLAS_NUM_THREADS", "99")
	req := dataProfileRequest()
	req.TimeoutMS = api.ExecutionTimeoutHardMaxMS
	req.Source = "import os, numpy as np, pandas as pd\ndef main(input, context):\n    frame = pd.DataFrame({'value': input})\n    frame.to_csv(os.path.join(context['output_dir'], 'report.csv'), index=False)\n    return {'sum': int(frame.value.sum()), 'mean': float(np.mean(input)), 'profile': context['profile'], 'threads': os.getenv('OPENBLAS_NUM_THREADS')}\n"
	req.Input = json.RawMessage("[1,2,3]")
	req.OutputFiles = []string{"report.csv"}
	result, err := NewWithProfile(api.ExecutionProfilePythonDataV1).Handle(context.Background(), req, nil, nil)
	if err != nil || result.Status != api.ExecutionStatusSucceeded {
		t.Fatalf("data execution = %+v, %v", result, err)
	}
	var value struct {
		Sum     int     `json:"sum"`
		Mean    float64 `json:"mean"`
		Profile string  `json:"profile"`
		Threads string  `json:"threads"`
	}
	if err := json.Unmarshal(result.Result, &value); err != nil {
		t.Fatal(err)
	}
	if value.Sum != 6 || value.Mean != 2 || value.Profile != "python-data-v1" || value.Threads != "1" || len(result.Artifacts) != 1 || string(result.Artifacts[0].Content) != "value\n1\n2\n3\n" {
		t.Fatalf("output=%s/artifacts=%+v", result.Result, result.Artifacts)
	}

	// Even on a machine with installed packages, standard remains isolated.
	req.Profile = api.ExecutionProfileStandard
	req.Version = executionproto.Version
	req.OutputFiles = nil
	req.Source = "import importlib.util\ndef main(input, context): return {'available': importlib.util.find_spec('numpy') is not None}"
	plain, err := New().Handle(context.Background(), req, nil, nil)
	if err != nil || plain.Status != api.ExecutionStatusSucceeded || string(plain.Result) != `{"available":false}` {
		t.Fatalf("standard profile gained site packages: %+v, %v", plain, err)
	}

	// A data image missing its site packages must fail before tenant code.
	req = dataProfileRequest()
	req.TimeoutMS = api.ExecutionTimeoutHardMaxMS
	req.Source = "print('tenant-dispatched')\ndef main(input, context): return input"
	e := NewWithProfile(api.ExecutionProfilePythonDataV1)
	e.resolve = func(executionproto.Request) (string, []string, string, error) {
		return interpreter, []string{"-I", "-S", "-c", pythonWrapper}, pythonSourceName, nil
	}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 31*time.Second)
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- executionproto.Serve(ctx, server, e.Handle) }()
	protocol, err := executionproto.NewClient(client)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := protocol.Execute(ctx, req)
	if err != nil || missing.Status != api.ExecutionStatusFailed || len(missing.Stdout) != 0 || (len(missing.Result) != 0 && string(missing.Result) != "null") || !strings.Contains(string(missing.Stderr), "PackageNotFoundError") {
		t.Fatalf("missing packages: status=%s, result=%s, stdout=%q, stderr=%s, error=%v", missing.Status, missing.Result, missing.Stdout, missing.Stderr, err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}
