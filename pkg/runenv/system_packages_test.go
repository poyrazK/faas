package runenv

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestSystemPackagesRejectBuildSyntaxAndAmbiguousPins(t *testing.T) {
	for _, pins := range [][]string{
		{"poppler-utils"}, {"poppler-utils>=26"}, {"poppler-utils=26.10.0-r0;id"}, {"--repository=https://evil.invalid"},
		{"https://evil.invalid/pkg.apk"}, {"pkg=1.0-r1\nRUN id"}, {"pkg=1.0-r1", "pkg=2.0-r0"},
		{"@repo/pkg=1.0-r1"}, {"pkg=1.0-r1 "}, make([]string, api.ExecutionEnvironmentMaxSystemPackages+1),
	} {
		if Validate(api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, SystemPackages: pins}) == nil {
			t.Fatalf("accepted unsafe pins %q", pins)
		}
	}
}

func TestSystemOnlyRecipesCanonicalizeAndUseAnExecFormInstaller(t *testing.T) {
	base := "registry.example/python@sha256:" + strings.Repeat("a", 64)
	r := api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimePython313, SystemPackages: []string{"zlib=1.3.1-r1", "poppler-utils=26.10.0-r0"}}
	for _, runtime := range []api.ExecutionRuntime{api.ExecutionRuntimePython313, api.ExecutionRuntimeNode24} {
		r.Runtime = runtime
		if err := Validate(r); err != nil {
			t.Fatal(err)
		}
		first, err := Digest(r, base)
		if err != nil {
			t.Fatal(err)
		}
		swapped := r
		swapped.SystemPackages = []string{r.SystemPackages[1], r.SystemPackages[0]}
		second, _ := Digest(swapped, base)
		if first != second {
			t.Fatal("package order changed identity")
		}
		id := uuid.NewString()
		source, err := SourceTar(id, r, base)
		if err != nil {
			t.Fatal(err)
		}
		again, _ := SourceTar(id, swapped, base)
		if !bytes.Equal(source, again) {
			t.Fatal("canonical source changed")
		}
		gz, err := gzip.NewReader(bytes.NewReader(source))
		if err != nil {
			t.Fatal(err)
		}
		reader := tar.NewReader(gz)
		found := false
		for {
			h, e := reader.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			data, _ := io.ReadAll(reader)
			if h.Name != "Dockerfile" {
				continue
			}
			text := string(data)
			if strings.Contains(text, "pip ") || strings.Contains(text, "npm ci") {
				t.Fatal("packages-only recipe invoked a language installer")
			}
			for _, line := range strings.Split(text, "\n") {
				if !strings.HasPrefix(line, "RUN ") {
					continue
				}
				var args []string
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "RUN ")), &args) != nil {
					t.Fatal("invalid exec-form command", line)
				}
				if args[0] == "python3" || args[0] == "node" {
					continue
				}
				if strings.Join(args, " ") != "apk add --no-cache -- poppler-utils=26.10.0-r0 zlib=1.3.1-r1" {
					t.Fatal("unsafe installer command", line)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("no package installer")
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLanguageOnlyRecipesPreserveLegacyIdentity(t *testing.T) {
	r := pythonRecipe()
	base := "registry.example/python@sha256:" + strings.Repeat("a", 64)
	old, _ := json.Marshal(struct {
		Format  string
		Base    string
		Request api.CreateExecutionEnvironmentRequest
	}{"run-environment/v1", base, r})
	expected := sha256.Sum256(old)
	actual, err := Digest(r, base)
	if err != nil || actual != hex.EncodeToString(expected[:]) {
		t.Fatal("legacy admission identity changed", actual, err)
	}
}
