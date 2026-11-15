// adr: 20261110150000
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeImage writes files (path -> "" for a regular file, "->target" for a
// symlink) under a temporary root.
func fakeImage(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, v := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if target, ok := strings.CutPrefix(v, "->"); ok {
			if err := os.Symlink(target, full); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(full, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestJavaWithoutInstrument(t *testing.T) {
	fullJDK := map[string]string{
		"usr/lib/jvm/temurin-21/bin/java":             "",
		"usr/lib/jvm/temurin-21/lib/libinstrument.so": "",
		"usr/lib/jvm/temurin-21/lib/server/libjvm.so": "",
		"etc/alternatives/java":                       "->/usr/lib/jvm/temurin-21/bin/java",
		"usr/bin/java":                                "->/etc/alternatives/java",
	}
	jlinked := map[string]string{
		"opt/jre/bin/java":             "",
		"opt/jre/lib/server/libjvm.so": "",
	}
	java8 := map[string]string{
		"opt/java/openjdk/bin/java":                       "",
		"opt/java/openjdk/jre/lib/amd64/libinstrument.so": "",
	}
	appLocal := map[string]string{
		"srv/runtime/bin/java": "",
	}
	merge := func(ms ...map[string]string) map[string]string {
		out := map[string]string{}
		for _, m := range ms {
			for k, v := range m {
				out[k] = v
			}
		}
		return out
	}
	for _, tc := range []struct {
		name     string
		files    map[string]string
		env      []string
		commands []string
		want     string
	}{
		{name: "no JVM", files: map[string]string{"usr/bin/node": ""}, env: []string{"PATH=/usr/bin"}},
		{name: "full JDK through Debian alternatives", files: fullJDK, env: []string{"PATH=/usr/bin"}, commands: []string{"java"}},
		{name: "jlink runtime without java.instrument", files: jlinked, env: []string{"PATH=/usr/bin"}, commands: []string{"/opt/jre/bin/java"}, want: "/opt/jre/bin/java"},
		{name: "jlink runtime found without the command", files: jlinked, env: []string{"PATH=/usr/bin"}, commands: []string{"/bin/sh"}, want: "/opt/jre/bin/java"},
		{name: "jlink runtime through JAVA_HOME", files: map[string]string{"srv/rt/bin/java": ""}, env: []string{"JAVA_HOME=/srv/rt"}, want: "/srv/rt/bin/java"},
		{name: "Java 8 layout", files: java8, env: []string{"PATH=/opt/java/openjdk/bin"}, commands: []string{"java"}},
		{name: "one bad JVM among good ones", files: merge(fullJDK, jlinked), env: []string{"PATH=/usr/bin"}, commands: []string{"java"}, want: "/opt/jre/bin/java"},
		{name: "JVM only the command names", files: appLocal, env: []string{"PATH=/usr/bin"}, commands: []string{"/srv/runtime/bin/java", "-jar", "app.jar"}, want: "/srv/runtime/bin/java"},
		{name: "a missing command is not a JVM", files: map[string]string{}, env: []string{"PATH=/usr/bin"}, commands: []string{"/nope/bin/java"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fakeImage(t, tc.files)
			if got := javaWithoutInstrument(root, tc.env, tc.commands); got != tc.want {
				t.Fatalf("javaWithoutInstrument = %q, want %q", got, tc.want)
			}
		})
	}
}

// A jlink runtime without java.instrument refuses to start when any agent is
// named, so the stamp leaves the agent out and keeps the other preloads.
func TestStampRestoreReseedEnvLeavesOutAgentForJVMWithoutInstrument(t *testing.T) {
	root := fakeImage(t, map[string]string{"opt/jre/bin/java": ""})
	old := restoreReseedImageRoot
	restoreReseedImageRoot = root
	t.Cleanup(func() { restoreReseedImageRoot = old })

	env := stampRestoreReseedEnv([]string{"PATH=/usr/bin", "JAVA_TOOL_OPTIONS=-Xss1m"}, "/run/guest-init/rng", "/run/guest-init/rng.sock", "/opt/jre/bin/java")
	if got := envValue(env, "JAVA_TOOL_OPTIONS"); got != "-Xss1m" {
		t.Fatalf("JAVA_TOOL_OPTIONS = %q, want the workload's own value untouched", got)
	}
	if !strings.Contains(envValue(env, "NODE_OPTIONS"), "node-reseed.cjs") || envValue(env, "PYTHONPATH") == "" || envValue(env, restoreReseedSocketEnv) == "" {
		t.Fatalf("other preloads dropped: %v", env)
	}

	// The same image with java.instrument gets the agent.
	if err := os.MkdirAll(filepath.Join(root, "opt/jre/lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "opt/jre/lib/libinstrument.so"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	env = stampRestoreReseedEnv([]string{"PATH=/usr/bin"}, "/run/guest-init/rng", "/run/guest-init/rng.sock", "/opt/jre/bin/java")
	if !strings.Contains(envValue(env, "JAVA_TOOL_OPTIONS"), "-javaagent:/run/guest-init/rng/java-reseed.jar") {
		t.Fatalf("JAVA_TOOL_OPTIONS = %q, want the agent", envValue(env, "JAVA_TOOL_OPTIONS"))
	}
}
