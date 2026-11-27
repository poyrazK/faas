package rootfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type debugfsFakeRunner struct {
	argv   [][]string
	script string
	out    string
}

func (r *debugfsFakeRunner) Run(_ context.Context, argv []string) error {
	r.argv = append(r.argv, argv)
	return nil
}

func (r *debugfsFakeRunner) CombinedOutput(_ context.Context, argv []string) ([]byte, error) {
	r.argv = append(r.argv, argv)
	if len(argv) == 5 && argv[0] == "debugfs" && argv[2] == "-f" {
		body, err := os.ReadFile(argv[3])
		if err != nil {
			return nil, err
		}
		r.script = string(body)
	}
	return []byte(r.out), nil
}

// boa multi-service deploy (2026-11-21): the post-mkfs pass writes the
// trusted opaque xattr for each marked directory and removes its marker,
// and a debugfs request failure (reported with exit 0) fails the build.
func TestApplyDeferredOverlayOpacity(t *testing.T) {
	staging := t.TempDir()
	for _, dir := range []string{"upper", "upper/etc/fonts", "upper/usr/share/my fonts"} {
		if err := os.MkdirAll(filepath.Join(staging, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := markDeferredOverlayOpaque(filepath.Join(staging, filepath.FromSlash(dir))); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(staging, "upper", "srv", deferredOpaqueMarker), 0o755); err != nil {
		t.Fatal(err) // a directory with the marker's name is not a marker
	}
	runner := &debugfsFakeRunner{out: "debugfs 1.47.0 (5-Feb-2023)\ndebugfs: ea_set \"/upper\" trusted.overlay.opaque y\n"}
	b := NewBuilder(runner)
	if err := b.applyDeferredOverlayOpacity(t.Context(), staging, "/tmp/app.ext4"); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		`ea_set "/upper" trusted.overlay.opaque y`,
		`rm "/upper/.faas-overlay-opaque"`,
		`ea_set "/upper/etc/fonts" trusted.overlay.opaque y`,
		`rm "/upper/etc/fonts/.faas-overlay-opaque"`,
		`ea_set "/upper/usr/share/my fonts" trusted.overlay.opaque y`,
		`rm "/upper/usr/share/my fonts/.faas-overlay-opaque"`,
		"",
	}, "\n")
	if runner.script != want {
		t.Fatalf("debugfs script:\n%s\nwant:\n%s", runner.script, want)
	}
	if got := runner.argv[0]; got[0] != "debugfs" || got[1] != "-w" || got[4] != "/tmp/app.ext4" {
		t.Fatalf("argv = %v", got)
	}

	runner.out = "debugfs 1.47.0 (5-Feb-2023)\nea_set: Extended attribute key not found\n"
	if err := b.applyDeferredOverlayOpacity(t.Context(), staging, "/tmp/app.ext4"); err == nil || !strings.Contains(err.Error(), "ea_set") {
		t.Fatalf("debugfs request failure accepted: %v", err)
	}
}

func TestApplyDeferredOverlayOpacityNoMarkersRunsNothing(t *testing.T) {
	runner := &debugfsFakeRunner{}
	if err := NewBuilder(runner).applyDeferredOverlayOpacity(t.Context(), t.TempDir(), "/tmp/app.ext4"); err != nil || len(runner.argv) != 0 {
		t.Fatalf("err=%v argv=%v", err, runner.argv)
	}
}

func TestDeferredOpacityRefusesUnquotablePath(t *testing.T) {
	if _, err := deferredOpacityScript([]string{"/upper/a\"b"}); err == nil {
		t.Fatal("quote in path accepted")
	}
}

func TestOverlayLayerRefusesReservedMarkerName(t *testing.T) {
	for _, name := range []string{"etc/" + deferredOpaqueMarker, "etc/.wh." + deferredOpaqueMarker} {
		err := applyLayerGzForBuild(t.TempDir(), gzLayer(t, []entry{{name: name, body: "x"}}), false, nil, nil)
		if !errors.Is(err, errReservedOverlayMarker) {
			t.Fatalf("%s: err = %v, want reserved-name refusal", name, err)
		}
	}
	// An independent sidecar root has no overlay markers; the name is data.
	if err := applyLayerGzForBuild(t.TempDir(), gzLayer(t, []entry{{name: "etc/" + deferredOpaqueMarker, body: "x"}}), true, nil, nil); err != nil {
		t.Fatal(err)
	}
}
