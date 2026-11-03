package apidserver_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// The audit HMAC key keys every audit row's email hash. apid boots only
// with a key it can keep: no zero-key fallback, no in-process-only key.
func TestAuditHMACKeyLoaderRefusesToBootWithoutAKey(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}

	missing := filepath.Join(dir, "absent", "audit-hmac.key")
	key, err := apidserver.LoadOrGenerateAuditHMACKey(env(map[string]string{"FAAS_AUDIT_HMAC_KEY_FILE": missing}), log)
	if err == nil || key != nil || !strings.Contains(err.Error(), "refusing to start") {
		t.Fatalf("missing key dir = %x, %v; want a refusal", key, err)
	}

	minted := filepath.Join(dir, "audit-hmac.key")
	first, err := apidserver.LoadOrGenerateAuditHMACKey(env(map[string]string{"FAAS_AUDIT_HMAC_KEY_FILE": minted}), log)
	if err != nil || len(first) != 32 {
		t.Fatalf("first boot = %x, %v; want a minted 32-byte key", first, err)
	}
	again, err := apidserver.LoadOrGenerateAuditHMACKey(env(map[string]string{"FAAS_AUDIT_HMAC_KEY_FILE": minted}), log)
	if err != nil || string(again) != string(first) {
		t.Fatal("the minted key did not survive a restart")
	}

	if os.Geteuid() != 0 {
		readOnly := filepath.Join(dir, "read-only")
		if err := os.Mkdir(readOnly, 0o500); err != nil {
			t.Fatal(err)
		}
		_, err := apidserver.LoadOrGenerateAuditHMACKey(env(map[string]string{"FAAS_AUDIT_HMAC_KEY_FILE": filepath.Join(readOnly, "audit-hmac.key")}), log)
		if err == nil || !strings.Contains(err.Error(), "refusing to start") {
			t.Fatalf("unwritable key dir = %v; want a refusal, not an in-process key", err)
		}
	}

	if _, err := apidserver.LoadOrGenerateAuditHMACKey(env(map[string]string{"FAAS_AUDIT_HMAC_KEY": "abcd"}), log); err == nil {
		t.Fatal("a short FAAS_AUDIT_HMAC_KEY was accepted")
	}
}
