// adr: 946
package main

import (
	"bytes"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/durableentity/validatorbundle"
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseHookPackagesAndPreservesDeploymentIdentity(t *testing.T) {
	root := t.TempDir()
	entry := "validator.mjs"
	if err := os.WriteFile(filepath.Join(root, entry), []byte("export default () => ({protocol_version:1,valid:true})"), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "registry.json")
	app, deployment := uuid.NewString(), uuid.NewString()
	args := []string{"--app", app, "--deployment", deployment, "--runtime", "node22", "--root", root, "--entrypoint", entry, "--file", entry, "--output", output}
	var stdout bytes.Buffer
	if err := run(args, &stdout); err != nil {
		t.Fatal(err)
	}
	loaded, err := validatorbundle.Load(output)
	if err != nil || len(loaded) != 1 {
		t.Fatal(loaded, err)
	}
	b := loaded[deployment]
	if b.SHA256 != validatorbundle.Hash(b) || b.AppID != app {
		t.Fatal(b)
	}
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err = run(args, &stdout); err == nil {
		t.Fatal("replaced existing artifact")
	}
	after, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("existing registry altered", err)
	}
	if err = os.WriteFile(filepath.Join(root, entry), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := packageBundle(root, entry, []string{entry})
	if err != nil {
		t.Fatal(err)
	}
	changed.AppID, changed.DeploymentID, changed.Runtime = app, deployment, b.Runtime
	changed.SHA256 = validatorbundle.Hash(changed)
	if _, err = validatorbundle.Merge(loaded, changed); err == nil {
		t.Fatal("changed bytes reused deployment")
	}
}

func TestReleaseHookRejectsUnlistedAndEscapingFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "entry.mjs"), []byte("export default () => 1"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, names := range [][]string{nil, {"../entry.mjs"}, {"entry.mjs", "entry.mjs"}, {"missing.mjs"}} {
		if _, err := packageBundle(root, "entry.mjs", names); err == nil {
			t.Fatal("invalid file list accepted", names)
		}
	}
	if err := os.Symlink(filepath.Join(root, "entry.mjs"), filepath.Join(root, "link.mjs")); err != nil {
		t.Fatal(err)
	}
	if _, err := packageBundle(root, "link.mjs", []string{"link.mjs"}); err == nil {
		t.Fatal("symlink accepted")
	}
}
