package main

import (
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func TestDisabledNodeDoesNotCreateDataDirectory(t *testing.T) {
	const helper = "FAAS_TEST_DISABLED_NODE_DIRECTORY"
	if dir := os.Getenv(helper); dir != "" {
		flag.CommandLine = flag.NewFlagSet("object-storaged", flag.ContinueOnError)
		os.Args = []string{"object-storaged", "--data-dir", dir}
		if err := run(slog.New(slog.NewTextHandler(io.Discard, nil))); !errors.Is(err, objectstorage.ErrOwnedNodeDisabled) {
			t.Fatal("disabled startup was not rejected", err)
		}
		return
	}
	for _, tc := range []struct{ name, master, server string }{
		{"default-off", "", ""},
		{"master-only", "1", ""},
		{"server-only", "", "1"},
		{"invalid-master", "invalid", "1"},
		{"invalid-server", "1", "invalid"},
		{"false-server", "1", "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "must-not-be-created")
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestDisabledNodeDoesNotCreateDataDirectory$")
			for _, item := range os.Environ() {
				name, _, _ := strings.Cut(item, "=")
				if !objectstorage.IsOwnedNodeFeatureEnv(name) {
					cmd.Env = append(cmd.Env, item)
				}
			}
			// Unrelated capabilities must not grant permission to serve.
			cmd.Env = append(cmd.Env, helper+"="+dir, objectstorage.OwnedNodeEnabledEnv+"="+tc.master, objectstorage.OwnedNodeServerEnabledEnv+"="+tc.server,
				objectstorage.OwnedNodeBackendsEnabledEnv+"=1", objectstorage.OwnedNodeSettlementsEnabledEnv+"=1", objectstorage.OwnedNodeUsageCountersEnabledEnv+"=1")
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatal(string(output), err)
			}
			if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("disabled startup touched its data directory", err)
			}
		})
	}
}
