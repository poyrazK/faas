package e2etest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConsole(t *testing.T, dir, name, body string, mtime time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return p
}

// Only this harness's guests: a shared node carries consoles from earlier
// runs, and dumping those would attribute someone else's kernel panic to the
// current failure.
func TestRecentConsoleTails_OnlyConsolesSinceStart(t *testing.T) {
	dir := t.TempDir()
	start := time.Now().Add(-time.Minute)
	old := writeConsole(t, dir, "vm-old.console", "old guest\n", start.Add(-time.Hour))
	fresh := writeConsole(t, dir, "vm-new.console", "guest-init: mounted rootfs\n", start.Add(time.Second))
	writeConsole(t, dir, "not-a-console.log", "ignored\n", start.Add(time.Second))

	tails, err := recentConsoleTails(dir, start)
	if err != nil {
		t.Fatal(err)
	}
	if len(tails) != 1 || tails[0].Path != fresh {
		t.Fatalf("got %d tails %v; want only %s (not the pre-start %s)", len(tails), paths(tails), fresh, old)
	}
	if !strings.Contains(tails[0].Tail, "mounted rootfs") {
		t.Errorf("tail lost the console text: %q", tails[0].Tail)
	}
}

// A long console must be trimmed to its end — the failure is at the end.
func TestRecentConsoleTails_KeepsTheEnd(t *testing.T) {
	dir := t.TempDir()
	start := time.Now().Add(-time.Minute)
	body := strings.Repeat("x", 3*consoleTailBytes) + "\nguest-init: build failed: DNS preflight\n"
	writeConsole(t, dir, "vm-a.console", body, start.Add(time.Second))

	tails, err := recentConsoleTails(dir, start)
	if err != nil || len(tails) != 1 {
		t.Fatalf("tails=%d err=%v", len(tails), err)
	}
	if len(tails[0].Tail) > consoleTailBytes || !strings.HasSuffix(strings.TrimRight(tails[0].Tail, "\n"), "DNS preflight") {
		t.Errorf("tail is %d bytes and ends %q; want <= %d bytes ending in the last line",
			len(tails[0].Tail), tails[0].Tail[max(0, len(tails[0].Tail)-30):], consoleTailBytes)
	}
	if tails[0].Bytes != int64(len(body)) {
		t.Errorf("Bytes = %d, want the whole file %d", tails[0].Bytes, len(body))
	}
}

// A host without vmmd has no console directory; that is not a failure.
func TestRecentConsoleTails_MissingDirIsEmpty(t *testing.T) {
	tails, err := recentConsoleTails(filepath.Join(t.TempDir(), "absent"), time.Now())
	if err != nil || len(tails) != 0 {
		t.Fatalf("missing dir: tails=%d err=%v; want none, nil", len(tails), err)
	}
	if r := renderConsoleTails(nil); r != "" {
		t.Errorf("render of nothing = %q, want empty so DumpLogs stays quiet", r)
	}
}

func TestRenderConsoleTails_NamesEachGuest(t *testing.T) {
	r := renderConsoleTails([]consoleTail{{Path: "/var/log/faas/vm-abc.console", Bytes: 12, Tail: "panic\n"}})
	for _, want := range []string{"vm-abc.console", "12 bytes", "panic"} {
		if !strings.Contains(r, want) {
			t.Errorf("render lacks %q: %q", want, r)
		}
	}
}

func paths(ts []consoleTail) []string {
	out := make([]string, len(ts))
	for i, c := range ts {
		out[i] = c.Path
	}
	return out
}
