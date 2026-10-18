// adr: 1344 — off-host backup of the control plane's host-local state (§8).
package cpstate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

// writeTree creates files under root with the given contents and modes.
func writeTree(t *testing.T, root string, files map[string]os.FileMode) {
	t.Helper()
	for rel, mode := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("content of "+rel), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
}

func newIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSealRestoreRoundTripsToTheStandbyStage(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]os.FileMode{
		"etc/faas/secrets/session.key":         0o400,
		"etc/faas/secrets/host.age":            0o400,
		"etc/faas/secrets/schedd/schedd.env":   0o400,
		"etc/faas/tls/ca/ca.key":               0o400,
		"etc/faas/sealed.env":                  0o640,
		"var/lib/faas/audit-hmac.key":          0o600,
		"var/lib/faas/unrelated-runtime-state": 0o644,
	})
	spec := Spec{
		Paths: []string{
			filepath.Join(root, "etc/faas/secrets"),
			filepath.Join(root, "etc/faas/tls"),
			filepath.Join(root, "etc/faas/sealed.env"),
			filepath.Join(root, "var/lib/faas/audit-hmac.key"),
			filepath.Join(root, "etc/faas/missing.env"),
		},
		Exclude: []string{filepath.Join(root, "etc/faas/secrets/host.age")},
	}
	operator := newIdentity(t)
	var bundle bytes.Buffer
	sealed, err := Seal(&bundle, spec, []age.Recipient{operator.Recipient()}, "cp-1", time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if len(sealed.Files) != 5 {
		t.Fatalf("sealed %d files, want 5 (host.age excluded, missing skipped, unrelated not selected): %+v", len(sealed.Files), sealed.Files)
	}
	if bytes.Contains(bundle.Bytes(), []byte("content of")) {
		t.Fatal("bundle holds plaintext file contents")
	}

	stage := filepath.Join(t.TempDir(), "stage")
	restored, err := Restore(bytes.NewReader(bundle.Bytes()), []age.Identity{operator}, stage, false)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.Primary != "cp-1" || len(restored.Files) != 5 {
		t.Fatalf("restored index %+v", restored)
	}
	for _, f := range restored.Files {
		b64, err := os.ReadFile(filepath.Join(stage, "files", f.Path+".b64"))
		if err != nil {
			t.Fatalf("staged %s: %v", f.Path, err)
		}
		content, err := base64.StdEncoding.DecodeString(string(b64))
		if err != nil {
			t.Fatalf("staged %s is not base64: %v", f.Path, err)
		}
		if want := "content of " + strings.TrimPrefix(f.Path, root+"/"); string(content) != want {
			t.Errorf("%s = %q, want %q", f.Path, content, want)
		}
		if f.Owner == "" || f.Group == "" {
			t.Errorf("%s has no owner or group", f.Path)
		}
	}
	modes := map[string]string{}
	for _, f := range restored.Files {
		modes[strings.TrimPrefix(f.Path, root+"/")] = f.Mode
	}
	if modes["etc/faas/secrets/session.key"] != "0400" || modes["etc/faas/sealed.env"] != "0640" {
		t.Errorf("modes not preserved: %v", modes)
	}
	if _, err := os.Stat(filepath.Join(stage, "files", root, "etc/faas/secrets/host.age.b64")); err == nil {
		t.Error("the excluded host identity was staged")
	}

	// The stage index uses the keys control_plane_standby's takeover reads.
	var stageIdx map[string]json.RawMessage
	body, err := os.ReadFile(filepath.Join(stage, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &stageIdx); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"primary", "directories", "files"} {
		if _, ok := stageIdx[key]; !ok {
			t.Errorf("stage index missing %q", key)
		}
	}
	dirs := map[string]bool{}
	for _, d := range restored.Directories {
		dirs[strings.TrimPrefix(d.Path, root+"/")] = true
	}
	for _, want := range []string{"etc/faas/secrets", "etc/faas/secrets/schedd", "etc/faas/tls/ca", "var/lib/faas"} {
		if !dirs[want] {
			t.Errorf("directory %s not recorded: %v", want, dirs)
		}
	}
	if info, err := os.Stat(stage); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("stage directory is not root-only: %v %v", info.Mode(), err)
	}

	// An existing stage is replaced only on request.
	if _, err := Restore(bytes.NewReader(bundle.Bytes()), []age.Identity{operator}, stage, false); err == nil {
		t.Error("Restore overwrote an existing stage without force")
	}
	if _, err := Restore(bytes.NewReader(bundle.Bytes()), []age.Identity{operator}, stage, true); err != nil {
		t.Errorf("Restore with force: %v", err)
	}
}

func TestSealRefusesWithoutRecipients(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]os.FileMode{"a.key": 0o400})
	if _, err := Seal(&bytes.Buffer{}, Spec{Paths: []string{filepath.Join(root, "a.key")}}, nil, "cp-1", time.Now()); err == nil {
		t.Fatal("Seal wrote a bundle nobody can open")
	}
}

func TestRestoreRefusesAnotherIdentity(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]os.FileMode{"a.key": 0o400})
	var bundle bytes.Buffer
	if _, err := Seal(&bundle, Spec{Paths: []string{filepath.Join(root, "a.key")}}, []age.Recipient{newIdentity(t).Recipient()}, "cp-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(&bundle, []age.Identity{newIdentity(t)}, filepath.Join(t.TempDir(), "stage"), false); err == nil {
		t.Fatal("Restore opened a bundle with the wrong identity")
	}
}

// A bundle whose entries differ from its index — an unlisted file, a path that
// escapes the stage, a missing file — is refused before anything is staged.
func TestRestoreRefusesEntriesTheIndexDoesNotList(t *testing.T) {
	operator := newIdentity(t)
	build := func(index Index, entries map[string]string) []byte {
		var out bytes.Buffer
		enc, err := age.Encrypt(&out, operator.Recipient())
		if err != nil {
			t.Fatal(err)
		}
		gz := gzip.NewWriter(enc)
		tw := tar.NewWriter(gz)
		body, _ := json.Marshal(index)
		_ = writeTarFile(tw, indexName, body)
		for name, content := range entries {
			_ = writeTarFile(tw, name, []byte(content))
		}
		_ = tw.Close()
		_ = gz.Close()
		_ = enc.Close()
		return out.Bytes()
	}
	listed := Index{Version: IndexVersion, Primary: "cp-1", Files: []Entry{{Path: "/etc/faas/a.key", Owner: "root", Group: "root", Mode: "0400"}}}
	for name, bundle := range map[string][]byte{
		"unlisted file": build(listed, map[string]string{"files/etc/faas/a.key": "a", "files/etc/passwd": "x"}),
		"escaping path": build(Index{Version: IndexVersion, Files: []Entry{{Path: "/../etc/passwd"}}}, map[string]string{"files/../etc/passwd": "x"}),
		"missing file":  build(listed, nil),
		"wrong version": build(Index{Version: 99, Files: listed.Files}, map[string]string{"files/etc/faas/a.key": "a"}),
	} {
		t.Run(name, func(t *testing.T) {
			stage := filepath.Join(t.TempDir(), "stage")
			if _, err := Restore(bytes.NewReader(bundle), []age.Identity{operator}, stage, false); err == nil {
				t.Fatal("Restore accepted a bundle that does not match its index")
			}
			if _, err := os.Stat(stage); err == nil {
				t.Fatal("a refused bundle left a stage behind")
			}
		})
	}
}

func TestParseSpec(t *testing.T) {
	spec, err := ParseSpec(strings.NewReader("# control-plane state\n/etc/faas/secrets\n\n!/etc/faas/secrets/host.age\n/etc/faas/sealed.env\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Paths) != 2 || len(spec.Exclude) != 1 || spec.Exclude[0] != "/etc/faas/secrets/host.age" {
		t.Fatalf("spec = %+v", spec)
	}
	for _, bad := range []string{"relative/path\n", "/etc/faas/../shadow\n", "# only comments\n"} {
		if _, err := ParseSpec(strings.NewReader(bad)); err == nil {
			t.Errorf("ParseSpec(%q) accepted an invalid spec", bad)
		}
	}
}
