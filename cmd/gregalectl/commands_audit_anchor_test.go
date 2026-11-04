package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/auditanchor"
	"github.com/onebox-faas/faas/pkg/state"
)

func useAuditAnchorStore(t *testing.T, store *state.MemStore) {
	t.Helper()
	previous := auditAnchorStore
	auditAnchorStore = func(string, string) (auditanchor.Store, func(), error) { return store, func() {}, nil }
	t.Cleanup(func() { auditAnchorStore = previous })
}

func writeAuditKey(t *testing.T, fill byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit-hmac.key")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(bytes.Repeat([]byte{fill}, 32))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func captureAuditAnchorOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	oldOut, oldErr := osStdout, osStderr
	osStdout, osStderr = &out, &out
	t.Cleanup(func() { osStdout, osStderr = oldOut, oldErr })
	return &out
}

// adr: 20261026163015 — the timer's run seals a window, logs the anchor as
// one JSON line for the off-host log, and records the outcome for alerts;
// verify then recomputes the chain, and a run under another key refuses.
func TestAuditAnchorSealsLogsAndVerifies(t *testing.T) {
	store := state.NewMemStore()
	for i := range 3 {
		if err := store.InsertAuditLog(context.Background(), state.AuditLog{ID: uuid.New(), Kind: "account.login",
			ReceivedAt: time.Now().Add(-time.Hour + time.Duration(i)*time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	useAuditAnchorStore(t, store)
	key := writeAuditKey(t, 5)
	metrics := filepath.Join(t.TempDir(), "faas_audit_anchor.prom")
	out := captureAuditAnchorOutput(t)

	if code := cmdAuditAnchor([]string{"--key-file", key, "--metrics-file", metrics}); code != 0 {
		t.Fatalf("anchor = %d: %s", code, out)
	}
	var line struct {
		Msg  string `json:"msg"`
		Seq  int64  `json:"seq"`
		Rows int64  `json:"rows"`
		MAC  string `json:"mac"`
	}
	if err := json.Unmarshal(bytes.SplitN(out.Bytes(), []byte("\n"), 2)[0], &line); err != nil || line.Msg != "audit anchor sealed" || line.Seq != 1 || line.Rows != 3 || len(line.MAC) != 64 {
		t.Fatalf("log line = %+v (%v): %s", line, err, out)
	}
	prom, err := os.ReadFile(metrics)
	if err != nil || !strings.Contains(string(prom), "faas_audit_anchor_seq 1\n") || !strings.Contains(string(prom), "faas_audit_anchor_recent_findings 0\n") {
		t.Fatalf("metrics = %q, %v", prom, err)
	}

	out.Reset()
	if code := cmdAuditVerify([]string{"--key-file", key}); code != 0 || !strings.Contains(out.String(), "verified anchors 1 to 1 (1 anchors, 3 rows)") {
		t.Fatalf("verify = %d: %s", code, out)
	}
	other := writeAuditKey(t, 6)
	out.Reset()
	if code := cmdAuditVerify([]string{"--key-file", other}); code != 1 || !strings.Contains(out.String(), auditanchor.FindingKeyUnknown) {
		t.Fatalf("verify under another key = %d: %s", code, out)
	}
	out.Reset()
	if code := cmdAuditAnchor([]string{"--key-file", other}); code != 3 || !strings.Contains(out.String(), "another key") {
		t.Fatalf("anchor under another key = %d: %s", code, out)
	}
}

func TestLoadAuditHMACKeyFollowsApid(t *testing.T) {
	envKey := strings.Repeat("ab", 32)
	fileKey := writeAuditKey(t, 1)
	dbEnv := filepath.Join(t.TempDir(), "sealed.env")
	if err := os.WriteFile(dbEnv, []byte("DATABASE_URL=postgres://x\nFAAS_AUDIT_HMAC_KEY=\""+strings.Repeat("cd", 32)+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	getenv := func(env map[string]string) func(string) string { return func(k string) string { return env[k] } }
	for _, tc := range []struct {
		name        string
		env         map[string]string
		dbEnv, file string
		want        byte
		wantErr     bool
	}{
		{name: "environment first", env: map[string]string{"FAAS_AUDIT_HMAC_KEY": envKey}, dbEnv: dbEnv, file: fileKey, want: 0xab},
		{name: "then the db env file", dbEnv: dbEnv, file: fileKey, want: 0xcd},
		{name: "then the key file", file: fileKey, want: 1},
		{name: "the key file from the environment", env: map[string]string{"FAAS_AUDIT_HMAC_KEY_FILE": fileKey}, want: 1},
		{name: "a short key", env: map[string]string{"FAAS_AUDIT_HMAC_KEY": "abcd"}, wantErr: true},
		{name: "no key", file: filepath.Join(t.TempDir(), "missing"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, err := loadAuditHMACKey(tc.dbEnv, tc.file, getenv(tc.env))
			if tc.wantErr {
				if err == nil {
					t.Fatal("loaded a key, want an error")
				}
				return
			}
			if err != nil || len(key) != 32 || key[0] != tc.want {
				t.Fatalf("key = %x, %v; want 32 bytes of %x", key, err, tc.want)
			}
		})
	}
}
