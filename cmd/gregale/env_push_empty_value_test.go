package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// prod 2026-11-20: an `EMPTY=` line was refused by the API after the keys
// before it were saved, so the push half-applied and --restart never ran.
// Empty values are now skipped, with a warning, before anything is written.
func TestEnvPushSkipsEmptyValuesBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, ".env")
	if err := os.WriteFile(file, []byte("A=alpha\nEMPTY=\nB=bravo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	puts, mu := envPushServer(t)
	_, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"env", "push", "--app", "hello", "-f", file})
	restore()
	mu.Lock()
	got := strings.Join(*puts, ",")
	mu.Unlock()
	if code != 0 || got != "A,B" {
		t.Fatalf("exit %d, PUT keys %q; want 0 and A,B", code, got)
	}
	if !strings.Contains(stderr(), "EMPTY has no value and was not pushed") {
		t.Fatalf("stderr %q does not warn about EMPTY", stderr())
	}
}
