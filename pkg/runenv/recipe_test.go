package runenv

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"strings"
	"testing"
)

func pythonRecipe() api.CreateExecutionEnvironmentRequest {
	return api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, Requirements: "six==1.17.0 --hash=sha256:" + strings.Repeat("a", 64) + "\n"}
}
func nodeRecipe() api.CreateExecutionEnvironmentRequest {
	lock, _ := json.Marshal(map[string]any{"lockfileVersion": 3, "packages": map[string]any{"": map[string]any{}, "node_modules/pkg": map[string]any{"resolved": "https://registry.npmjs.org/pkg/-/pkg-1.0.0.tgz", "integrity": "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))}}})
	return api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimeNode24, PackageJSON: `{"dependencies":{"pkg":"1.0.0"}}`, PackageLock: string(lock)}
}
func TestRecipesRejectUnpinnedAndNonRegistryInputs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request api.CreateExecutionEnvironmentRequest
	}{
		{"unpinned", api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, Requirements: "six>=1"}},
		{"missing hash", api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, Requirements: "six==1.17.0"}},
		{"index injection", api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, Requirements: pythonRecipe().Requirements + "--index-url https://private.invalid"}},
		{"url", api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, Requirements: "https://example.com/pkg.whl --hash=sha256:" + strings.Repeat("a", 64)}},
		{"credentials", func() api.CreateExecutionEnvironmentRequest {
			r := nodeRecipe()
			r.PackageLock = strings.ReplaceAll(r.PackageLock, "https://registry.npmjs.org", "https://token@registry.npmjs.org")
			return r
		}()},
		{"private registry", func() api.CreateExecutionEnvironmentRequest {
			r := nodeRecipe()
			r.PackageLock = strings.ReplaceAll(r.PackageLock, "registry.npmjs.org", "127.0.0.1")
			return r
		}()},
		{"missing integrity", func() api.CreateExecutionEnvironmentRequest {
			r := nodeRecipe()
			r.PackageLock = strings.ReplaceAll(r.PackageLock, "sha512-", "sha256-")
			return r
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if Validate(tc.request) == nil {
				t.Fatal("unsafe recipe accepted")
			}
		})
	}
}
func TestSourceTarIsDeterministicAndHasFixedCommands(t *testing.T) {
	id := uuid.NewString()
	base := "registry.example/runtime@sha256:" + strings.Repeat("a", 64)
	for _, r := range []api.CreateExecutionEnvironmentRequest{pythonRecipe(), nodeRecipe()} {
		if err := Validate(r); err != nil {
			t.Fatal(err)
		}
		first, err := SourceTar(id, r, base)
		if err != nil {
			t.Fatal(err)
		}
		second, _ := SourceTar(id, r, base)
		if !bytes.Equal(first, second) {
			t.Fatal("unstable build input")
		}
		gz, err := gzip.NewReader(bytes.NewReader(first))
		if err != nil {
			t.Fatal(err)
		}
		tr := tar.NewReader(gz)
		files := map[string]string{}
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(tr)
			files[h.Name] = string(body)
		}
		if !strings.Contains(files["execution.json"], id) || !strings.HasPrefix(files["Dockerfile"], "FROM "+base+"\n") {
			t.Fatal(files)
		}
		if r.Runtime == api.ExecutionRuntimeNode24 && !strings.Contains(files["Dockerfile"], "--ignore-scripts") {
			t.Fatal("install scripts enabled")
		}
		a, _ := Digest(r, base)
		b, _ := Digest(r, strings.Replace(base, strings.Repeat("a", 64), strings.Repeat("b", 64), 1))
		if a == b {
			t.Fatal("toolchain missing from identity")
		}
	}
}
