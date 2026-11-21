package managedpostgres

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The managed_postgres_config Ansible role refuses a config whose backend
// fingerprints would strand existing databases. Its check script recomputes
// fingerprint() in Python; this pins the two together.
const ansibleConfigCheck = "../../deploy/ansible/roles/managed_postgres_config/files/managed-postgres-config-check.py"

func TestAnsibleConfigCheckFingerprintsMatch(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	config := Config{DefaultRegion: "us-east-2", Defaults: map[string]string{"us-east-2": "neon-us"}, Backends: []BackendConfig{
		{ID: "neon-us", Driver: "neon", Region: "us-east-2", Namespace: "org-abc-123",
			Settings: map[string]string{"region_id": "aws-us-east-2", "database_name": "gregale", "max_storage_bytes": "107374182400",
				"max_restore_window_seconds": "604800", "note": "a<b&c>d é"},
			PreviousSettings: []map[string]string{{"region_id": "aws-us-east-2", "database_name": "gregale", "max_storage_bytes": "21474836480",
				"max_restore_window_seconds": "604800", "note": "a<b&c>d é"}},
			SecretEnv: map[string]string{"api-key": "FAAS_NEON_API_KEY"}},
		{ID: "native", Driver: "firecracker", Region: "us-east-2", Namespace: "", Settings: nil},
	}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(python, ansibleConfigCheck, path, "--fingerprints").Output()
	if err != nil {
		t.Fatalf("check script: %v", err)
	}
	var want []string
	for _, backend := range config.Backends {
		want = append(want, backend.ID+" "+fingerprint(backend))
		previous, err := previousFingerprints(backend)
		if err != nil {
			t.Fatal(err)
		}
		for fp := range previous {
			want = append(want, backend.ID+" "+fp)
		}
	}
	sort.Strings(want)
	got := strings.Fields(strings.TrimSpace(string(out)))
	var pairs []string
	for i := 0; i+1 < len(got); i += 2 {
		pairs = append(pairs, got[i]+" "+got[i+1])
	}
	sort.Strings(pairs)
	if strings.Join(pairs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("python fingerprints\n%s\nwant\n%s", strings.Join(pairs, "\n"), strings.Join(want, "\n"))
	}

	// The shape check refuses a secret in settings and an unknown default.
	bad := config
	bad.Backends = append([]BackendConfig{}, config.Backends...)
	bad.Backends[0].Settings = map[string]string{"api_key": "sk-live"}
	bad.Defaults = map[string]string{"us-east-2": "missing"}
	raw, _ = json.Marshal(bad)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := exec.Command(python, ansibleConfigCheck, path).CombinedOutput()
	if err == nil || !strings.Contains(string(result), "looks like a secret") || !strings.Contains(string(result), "unknown backend missing") {
		t.Fatalf("bad config accepted or misreported (%v): %s", err, result)
	}
	// An unquoted YAML number in settings renders as a JSON number, which
	// apid cannot decode.
	numeric := strings.Replace(string(raw), `"api_key":"sk-live"`, `"max_storage_bytes":107374182400`, 1)
	if err := os.WriteFile(path, []byte(numeric), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = exec.Command(python, ansibleConfigCheck, path).CombinedOutput()
	if err == nil || !strings.Contains(string(result), "must be a string") {
		t.Fatalf("numeric setting accepted (%v): %s", err, result)
	}
}
