package e2etest

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

func TestHarnessArtifactHandoffUsesSharedAppRoot(t *testing.T) {
	tmp := t.TempDir()
	previous := currentHarness
	currentHarness = &Harness{BinDir: tmp, ImagedTmp: filepath.Join(tmp, "apps")}
	t.Cleanup(func() { currentHarness = previous })
	imagedRoot, ok := envValue(t, imagedEnv(t, "postgres:///test", "", currentHarness.ImagedTmp, tmp), "FAAS_APPS_ROOT")
	if !ok {
		t.Fatal("imaged artifact root missing")
	}
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	t.Setenv("FAAS_STORAGE_ROOT", filepath.Join(tmp, "fc"))
	t.Setenv("FAAS_APPS_ROOT", imagedRoot)
	publisher, err := storage.BackendFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	const key = "apps/fixture/deployment.ext4"
	const body = "published layer"
	if err := publisher.Put(context.Background(), key, strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	readers := map[string][]string{
		"vmmd":   vmmdEnv("postgres:///test", filepath.Join(tmp, "vmmd.toml"), ""),
		"schedd": testEnvCommon("postgres:///test"),
	}
	for name, env := range readers {
		t.Run(name, func(t *testing.T) {
			root, ok := envValue(t, env, "FAAS_APPS_ROOT")
			if !ok {
				t.Fatal("artifact reader would use the production default")
			}
			t.Setenv("FAAS_APPS_ROOT", root)
			consumer, err := storage.BackendFromEnv()
			if err != nil {
				t.Fatal(err)
			}
			r, err := consumer.Get(context.Background(), key)
			if err != nil {
				t.Fatalf("cannot read the layer imaged published: %v", err)
			}
			defer r.Close()
			got, err := io.ReadAll(r)
			if err != nil || string(got) != body {
				t.Fatalf("layer handoff: body=%q err=%v", got, err)
			}
		})
	}
}
