package reposcan_test

import (
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/reposcan"
)

func TestComposeEnvironmentOverrideMergesEquivalentForms(t *testing.T) {
	forms := []struct{ name, base, override string }{
		{"maps", "{KEEP_ME: base, LOG_LEVEL: info, FROM_HOST: original}", "{LOG_LEVEL: debug, FROM_HOST: null}"},
		{"lists", "[KEEP_ME=base, LOG_LEVEL=info, FROM_HOST=original]", "[LOG_LEVEL=debug, FROM_HOST]"},
		{"map to list", "{KEEP_ME: base, LOG_LEVEL: info, FROM_HOST: original}", "[LOG_LEVEL=debug, FROM_HOST]"},
		{"list to map", "[KEEP_ME=base, LOG_LEVEL=info, FROM_HOST=original]", "{LOG_LEVEL: debug, FROM_HOST: null}"},
	}
	for _, tc := range forms {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"compose.yaml":          {Data: []byte("services:\n  web:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n    environment: " + tc.base + "\n")},
				"compose.override.yaml": {Data: []byte("services:\n  web:\n    environment: " + tc.override + "\n")},
			}
			env, err := reposcan.ReadComposeEnv(fsys)
			if err != nil {
				t.Fatal(err)
			}
			entries := env.Services["web"]
			if len(entries) != 3 || entries["KEEP_ME"] == nil || *entries["KEEP_ME"] != "base" || entries["LOG_LEVEL"] == nil || *entries["LOG_LEVEL"] != "debug" || entries["FROM_HOST"] != nil {
				t.Fatal("override lost inherited keys, precedence, or the host-value declaration")
			}
			scan, err := reposcan.Scan(fsys)
			if err != nil || len(scan.Workloads) != 1 || len(scan.Workloads[0].EnvKeys) != 3 {
				t.Fatalf("scan disagrees with the imported environment: workloads=%d err=%v", len(scan.Workloads), err)
			}
		})
	}
}

func TestComposeEnvironmentMergeDoesNotDependOnServiceName(t *testing.T) {
	fsys := fstest.MapFS{
		"compose.yaml":          {Data: []byte("services:\n  services:\n    image: nginx:1.27\n    environment: {KEEP_ME: base}\n")},
		"compose.override.yaml": {Data: []byte("services:\n  services:\n    environment: [LOG_LEVEL=debug]\n")},
	}
	env, err := reposcan.ReadComposeEnv(fsys)
	if err != nil || env.Services["services"]["KEEP_ME"] == nil || env.Services["services"]["LOG_LEVEL"] == nil {
		t.Fatalf("service name changed environment merge behavior: %v", err)
	}
}
