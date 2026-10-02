package region

import "testing"

func TestRuntimeLogArchiveKeys(t *testing.T) {
	for _, id := range []string{"", "eu-fsn1"} {
		key := RuntimeLogArchiveKey(id, "instance-1", "2026-10-02")
		got, instance, day, err := ParseRuntimeLogArchiveKey(key)
		if err != nil || got != id || instance != "instance-1" || day != "2026-10-02" {
			t.Fatalf("round trip: %q %q %q %v", got, instance, day, err)
		}
	}
	for _, key := range []string{"faas-logs/../2026/10/2026-10-02.jsonl.gz", "faas-logs/i/2026/09/2026-10-02.jsonl.gz", "faas-logs/i/2026/10/2026-10-40.jsonl.gz", "faas-logs/i/0000/01/0000-01-01.jsonl.gz", "regions/EU/faas-logs/i/2026/10/2026-10-02.jsonl.gz"} {
		if _, _, _, err := ParseRuntimeLogArchiveKey(key); err == nil {
			t.Fatalf("accepted %q", key)
		}
	}
}
