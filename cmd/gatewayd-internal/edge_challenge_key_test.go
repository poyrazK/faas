package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adr: 831 — amendment 17: gateways sign challenges with the fleet key when
// Ansible delivers one, else with their own session key.
func TestEdgeChallengeSecret(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	fleetHex := strings.Repeat("ab", 32)
	session := []byte("session-key-of-this-host-32bytes")
	lookup := func(path string) func(string) string {
		return func(key string) string {
			if key == "FAAS_EDGE_CHALLENGE_KEY_FILE" {
				return path
			}
			return ""
		}
	}

	if got, source, err := edgeChallengeSecret(lookup(""), session); err != nil || !bytes.Equal(got, session) || source != edgeChallengeKeySession {
		t.Fatalf("unset = %x, %q, %v; want the session key", got, source, err)
	}
	got, source, err := edgeChallengeSecret(lookup(write("fleet.key", fleetHex+"\n")), session)
	if err != nil || source != edgeChallengeKeyFleet || len(got) != 32 || got[0] != 0xab {
		t.Fatalf("fleet key = %x, %q, %v", got, source, err)
	}
	for name, path := range map[string]string{
		"missing file": filepath.Join(dir, "absent.key"),
		"not hex":      write("not-hex.key", strings.Repeat("zz", 32)),
		"too short":    write("short.key", strings.Repeat("ab", 16)),
		"empty":        write("empty.key", "\n"),
	} {
		if _, _, err := edgeChallengeSecret(lookup(path), session); err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "zz") {
			t.Errorf("%s: error repeats the file's content: %v", name, err)
		}
	}
}
