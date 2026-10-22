package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// The build boundary is intentionally rejected after the real upsert handler.
// This qualifies actual CLI planning/provisioning/inspection/teardown, not a VM.
func TestDevBucketCLICommandsMem(t *testing.T) {
	cli := os.Getenv("GREGALE_OBJECT_STORAGE_E2E_CLI")
	if cli == "" {
		t.Skip("GREGALE_OBJECT_STORAGE_E2E_CLI required")
	}
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	e := apidserver.SetupSecrets(t, api.PlanPro)
	apidserver.SetS3Flag(t, e, true)
	provider := &devCLIObjectProvider{FakeObjectProvider: newStorageTestObjectProvider(map[string][]byte{}, nil)}
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, provider, newStorageTestObjectProvider(nil, nil), "external"))
	created := make(chan api.DevSessionResponse, 2)
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/v1/dev/sessions/files-app" {
			out := httptest.NewRecorder()
			(*e.H()).ServeHTTP(out, r)
			var receipt api.DevSessionResponse
			if out.Code != 201 && out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &receipt) != nil {
				t.Errorf("upsert = %d: %s", out.Code, out.Body.String())
			} else {
				created <- receipt
			}
			api.WriteProblem(w, api.NewProblem(503, "object_storage_unavailable", "Fixture boundary", "Storage provisioned; VM build intentionally excluded"))
			return
		}
		(*e.H()).ServeHTTP(w, r)
	}))
	defer control.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"start":"node server.js"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) ([]byte, error) {
		t.Helper()
		command := exec.CommandContext(t.Context(), cli, append([]string{"--json"}, args...)...)
		command.Dir = dir
		command.Env = append(os.Environ(), "FAAS_API="+control.URL, "FAAS_TOKEN="+(*e.Key()), "FAAS_DEVELOPER_ID="+strings.Repeat("a", 32))
		var out, stderr bytes.Buffer
		command.Stdout, command.Stderr = &out, &stderr
		err := command.Run()
		if err != nil {
			t.Logf("CLI fixture result: %s", stderr.String())
		}
		return out.Bytes(), err
	}
	plan, err := run("dev", "setup", "--name", "files-app", "--bucket", "assets")
	if err != nil || provider.creates.Load() != 0 || !bytes.Contains(plan, []byte("GREGALE_S3_ASSETS")) {
		t.Fatalf("plan = %s %v", plan, err)
	}
	var first api.DevSessionResponse
	for i := range 2 {
		_, err := run("dev", "--name", "files-app", "--bucket", "assets", "--once", "--no-logs")
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 3 {
			t.Fatalf("build boundary exit = %v", err)
		}
		select {
		case receipt := <-created:
			if len(receipt.Buckets) != 1 {
				t.Fatalf("receipt = %+v", receipt)
			}
			if i == 0 {
				first = receipt
			} else if receipt.Buckets[0].Binding.ID != first.Buckets[0].Binding.ID {
				t.Fatal("actual CLI refresh replaced binding")
			}
		default:
			t.Fatal("actual CLI did not reach provisioning handler")
		}
	}
	info, err := run("dev", "info", "--name", "files-app")
	if err != nil || !bytes.Contains(info, []byte(first.Buckets[0].Bucket.ID)) {
		t.Fatalf("inspection = %s %v", info, err)
	}
	if provider.creates.Load() != 1 {
		t.Fatal("planning/inspection/refresh created extra bucket")
	}
	stopped, err := run("dev", "--stop", "--name", "files-app")
	if err != nil || !bytes.Contains(stopped, []byte("stopped")) {
		t.Fatalf("stop = %s %v", stopped, err)
	}
	rows, err := (*e.Store()).ListObjectBuckets(t.Context(), (*e.Acct()).ID, first.App.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("CLI cleanup = %v %v", rows, err)
	}
}

// HTTP handlers run in separate goroutines from the child-process driver.
// Use an atomic observer instead of inspecting the provider's mutable slice.
type devCLIObjectProvider struct {
	*apidserver.FakeObjectProvider
	creates atomic.Int32
}

func (p *devCLIObjectProvider) CreateBucket(ctx context.Context, bucket string) error {
	p.creates.Add(1)
	return p.FakeObjectProvider.CreateBucket(ctx, bucket)
}
