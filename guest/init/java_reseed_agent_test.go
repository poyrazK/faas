// adr: 20261110150000
package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The committed agent jar is the build of the committed source: its manifest
// names the source's SHA-256 (scripts/rng/build-java-reseed-agent.sh). A
// source change without a rebuild fails here.
func TestJavaReseedAgentMatchesItsSource(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("rngpreload", "java", "dev", "gregale", "reseed", "Agent.java"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(src)
	jar, err := restoreReseedAssets.ReadFile("rngpreload/" + restoreReseedJavaAgent)
	if err != nil {
		t.Fatalf("embedded agent: %v", err)
	}
	manifest := javaAgentManifest(t, jar)
	if got, want := manifest["Gregale-Source-SHA256"], hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("agent jar built from source %s, committed source is %s; run scripts/rng/build-java-reseed-agent.sh guest/init/rngpreload/java-reseed.jar", got, want)
	}
	for _, key := range []string{"Premain-Class", "Agent-Class"} {
		if manifest[key] != "dev.gregale.reseed.Agent" {
			t.Fatalf("%s = %q", key, manifest[key])
		}
	}
	// A JVM refuses to start when an agent it cannot load is named: every
	// class must be Java 8 bytecode (major version 52).
	r, err := zip.NewReader(bytes.NewReader(jar), int64(len(jar)))
	if err != nil {
		t.Fatal(err)
	}
	classes := 0
	for _, f := range r.File {
		if !strings.HasSuffix(f.Name, ".class") {
			continue
		}
		classes++
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		head := make([]byte, 8)
		_, err = rc.Read(head)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if major := int(head[6])<<8 | int(head[7]); major != 52 {
			t.Fatalf("%s is class file version %d, want 52 (Java 8)", f.Name, major)
		}
	}
	if classes == 0 {
		t.Fatal("agent jar has no classes")
	}
}

func TestJavaReseedAgentIsWrittenReadOnly(t *testing.T) {
	dir := t.TempDir()
	if err := writeRestoreReseedAssets(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, restoreReseedJavaAgent))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o444 {
		t.Fatalf("agent mode = %v, want read-only", info.Mode().Perm())
	}
}

// javaAgentManifest parses META-INF/MANIFEST.MF, joining 72-byte
// continuation lines.
func javaAgentManifest(t *testing.T, jar []byte) map[string]string {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(jar), int64(len(jar)))
	if err != nil {
		t.Fatal(err)
	}
	f, err := r.Open("META-INF/MANIFEST.MF")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	out := map[string]string{}
	var last string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.HasPrefix(line, " ") && last != "" {
			out[last] += line[1:]
			continue
		}
		if key, value, ok := strings.Cut(line, ": "); ok {
			out[key], last = value, key
		}
	}
	return out
}
