package project

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity/validatorbundle"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(name string, value any) {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(Filename, Config{Version: 1, Entrypoint: "server.mjs", Handlers: []Handler{{Namespace: "counters", SchemaVersion: 1, Module: "handlers.mjs", Export: "counter"}}, RestoreValidator: "gregale.validator.json"})
	for _, name := range []string{"server.mjs", "handlers.mjs", "validator.mjs"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("export default () => true;"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", map[string]any{"dependencies": map[string]string{"@gregale/sdk-node": "file:packages/gregale-sdk.tgz"}})
	write("gregale.validator.json", validatorbundle.Source{Runtime: api.ExecutionRuntimeNode22, Entrypoint: "validator.mjs", Files: []api.ExecutionFile{{Path: "validator.mjs", Content: []byte("export default () => true;")}}})
	if err := os.Mkdir(filepath.Join(root, "packages"), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(root, "packages/gregale-sdk.tgz"))
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tarball := tar.NewWriter(gz)
	for name, body := range map[string]string{
		"package/package.json":                   `{"name":"@gregale/sdk-node","exports":{"./durable-entities/runtime":{"import":"./dist/durable-entity-runtime.js"},"./durable-entities/testing":{"import":"./dist/durable-entity-testing.js"}}}`,
		"package/dist/durable-entity-runtime.js": "export {};",
	} {
		if err := tarball.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarball.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, close := range []func() error{tarball.Close, gz.Close, f.Close} {
		if err := close(); err != nil {
			t.Fatal(err)
		}
	}
	packed, err := os.ReadFile(filepath.Join(root, "packages/gregale-sdk.tgz"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha512.Sum512(packed)
	write("package-lock.json", map[string]any{"packages": map[string]any{
		"":                               map[string]any{"dependencies": map[string]string{"@gregale/sdk-node": "file:packages/gregale-sdk.tgz"}},
		"node_modules/@gregale/sdk-node": map[string]string{"resolved": "file:packages/gregale-sdk.tgz", "integrity": "sha512-" + base64.StdEncoding.EncodeToString(sum[:])},
	}})
	return root
}

func TestProjectReadyAndOrdinaryProject(t *testing.T) {
	if present, err := Check(t.TempDir()); present || err != nil {
		t.Fatalf("ordinary source should be unaffected: %v %v", present, err)
	}
	if present, err := Check(fixture(t)); !present || err != nil {
		t.Fatalf("prepared project rejected: %v %v", present, err)
	}
}

func TestProjectRefusesIncompleteOrStaleArtifacts(t *testing.T) {
	for _, name := range []string{Filename, "server.mjs", "handlers.mjs", "packages/gregale-sdk.tgz", "package-lock.json", "gregale.validator.json", "validator.mjs"} {
		t.Run(name, func(t *testing.T) {
			root := fixture(t)
			if err := os.WriteFile(filepath.Join(root, name), []byte("stale or invalid"), 0o600); err != nil {
				t.Fatal(err)
			}
			// JavaScript existence is checked without claiming to parse/execute it.
			if name == "server.mjs" || name == "handlers.mjs" {
				if err := os.Remove(filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
			}
			if present, err := Check(root); !present || err == nil {
				t.Fatalf("incomplete source accepted: %v %v", present, err)
			}
		})
	}
}

func TestProjectRejectsTraversalDuplicateNamespaceAndSymlink(t *testing.T) {
	for _, module := range []string{"../outside.mjs", "/outside.mjs", "handlers.ts", "sub/../handlers.mjs"} {
		root := fixture(t)
		body, err := json.Marshal(Config{Version: 1, Entrypoint: "server.mjs", Handlers: []Handler{{Namespace: "counters", SchemaVersion: 1, Module: module, Export: "counter"}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, Filename), body, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Check(root); err == nil {
			t.Fatalf("unsafe module accepted: %s", module)
		}
	}
	root := fixture(t)
	body, err := os.ReadFile(filepath.Join(root, Filename))
	if err != nil {
		t.Fatal(err)
	}
	var config Config
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	config.Handlers = append(config.Handlers, config.Handlers[0])
	body, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, Filename), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(root); err == nil {
		t.Fatal("duplicate namespace accepted")
	}
	root = fixture(t)
	if err := os.Remove(filepath.Join(root, "handlers.mjs")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "server.mjs"), filepath.Join(root, "handlers.mjs")); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(root); err == nil {
		t.Fatal("symlink module accepted")
	}
}

func TestClientDeclarationRequiresPreparedContractAndClientSDK(t *testing.T) {
	for _, module := range []string{"../contracts.mjs", "missing.mjs", "handlers.mjs"} {
		root := fixture(t)
		config := Config{Version: 1, Entrypoint: "server.mjs", Handlers: []Handler{{Namespace: "counters", SchemaVersion: 1, Module: "handlers.mjs", Export: "counter", Client: &ClientContract{Module: module, Export: "counterContract"}}}}
		body, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, Filename), body, 0o600); err != nil {
			t.Fatal(err)
		}
		// The existing fixture's SDK deliberately lacks the client export.
		if present, err := Check(root); !present || err == nil {
			t.Fatalf("unprepared client declaration accepted: %s", module)
		}
	}
}
