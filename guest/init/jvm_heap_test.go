package main

import (
	"os"
	"path/filepath"
	"testing"
)

func jvmRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "usr/lib/jvm/java-21/bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "java"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// B3: a JVM that sizes no heap gets 75% of the VM, not the JVM's 25%.
func TestStampJVMHeapEnv(t *testing.T) {
	root := jvmRoot(t)
	env := stampJVMHeapEnv(root, []string{"JAVA_TOOL_OPTIONS=-Dfoo=1"}, []string{"java", "-jar", "app.jar"})
	if got := envValue(env, "JAVA_TOOL_OPTIONS"); got != "-Dfoo=1 "+jvmHeapDefault {
		t.Fatalf("JAVA_TOOL_OPTIONS = %q, want the heap default appended", got)
	}
	for _, keep := range [][]string{
		{"JAVA_OPTS=-Xmx300m"},
		{"JDK_JAVA_OPTIONS=-XX:MaxRAMPercentage=50"},
		{jvmHeapOptOutEnv + "=off"},
	} {
		if got := envValue(stampJVMHeapEnv(root, keep, nil), "JAVA_TOOL_OPTIONS"); got != "" {
			t.Fatalf("env %v: JAVA_TOOL_OPTIONS = %q, want the workload's choice kept", keep, got)
		}
	}
	if got := envValue(stampJVMHeapEnv(root, nil, []string{"java", "-Xmx1g", "-jar", "a.jar"}), "JAVA_TOOL_OPTIONS"); got != "" {
		t.Fatalf("argv -Xmx: JAVA_TOOL_OPTIONS = %q, want none", got)
	}
	if got := envValue(stampJVMHeapEnv(t.TempDir(), nil, []string{"node", "server.js"}), "JAVA_TOOL_OPTIONS"); got != "" {
		t.Fatalf("no JVM: JAVA_TOOL_OPTIONS = %q, want none", got)
	}
}
