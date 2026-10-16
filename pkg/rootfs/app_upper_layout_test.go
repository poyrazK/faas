package rootfs

import (
	"archive/tar"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/scanview"
)

func TestStageAppUpperPreservesCustomerRootTree(t *testing.T) {
	for _, kind := range []string{"directory", "file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "payload"), []byte("customer content"), 0o640); err != nil {
				t.Fatal(err)
			}
			// This formerly collided with the private rename destination.
			if err := os.Mkdir(filepath.Join(root, ".faas-app-upper-source"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".faas-app-upper-source", "customer"), []byte("reserved-looking customer name"), 0o600); err != nil {
				t.Fatal(err)
			}
			upper := filepath.Join(root, "upper")
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(upper, 0o755)
				if err == nil {
					err = os.WriteFile(filepath.Join(upper, "nested"), []byte("customer upper directory"), 0o644)
				}
			case "file":
				err = os.WriteFile(upper, []byte("customer upper file"), 0o644)
			case "symlink":
				err = os.Symlink("/payload", upper)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := scanview.Snapshot(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			if err := stageAppUpper(root, false); err != nil {
				t.Fatal(err)
			}
			after, err := scanview.Snapshot(t.Context(), filepath.Join(root, "upper"))
			if err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatal("packaging changed the customer root's paths, content or link resolution")
			}
		})
	}
}

type inspectSidecarRootRunner struct {
	t *testing.T
}

func (r inspectSidecarRootRunner) Run(ctx context.Context, argv []string) error {
	r.t.Helper()
	var root string
	for i, arg := range argv {
		if arg == "-d" && i+1 < len(argv) {
			root = argv[i+1]
		}
	}
	if root == "" {
		r.t.Fatal("mkfs did not receive the assembled tree")
	}
	for _, name := range []string{"removed", ".wh.removed", "opaque/lower", "opaque/.wh..wh..opq"} {
		if _, err := os.Lstat(filepath.Join(root, "upper", name)); !os.IsNotExist(err) {
			r.t.Fatal("sidecar retained deleted entry", name, err)
		}
	}
	for _, name := range []string{"opaque/current", "upper/customer", ".faas-app-upper-source/customer"} {
		if data, err := os.ReadFile(filepath.Join(root, "upper", name)); err != nil || string(data) != "replacement" {
			r.t.Fatal("sidecar changed customer content", name, err)
		}
	}
	if _, err := scanview.Snapshot(ctx, filepath.Join(root, "upper")); err != nil {
		r.t.Fatal("sidecar root contains unsupported scanner entries", err)
	}
	return os.WriteFile(argv[len(argv)-2], []byte("TEST-EXT4"), 0o600)
}

func TestBuilderSidecarMaterializesAllLayerWhiteouts(t *testing.T) {
	guest := filepath.Join(t.TempDir(), "guest-init")
	if err := os.WriteFile(guest, []byte("INIT"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := api.AppManifest{Entrypoint: []string{"/bin/sidecar"}}
	b := NewBuilder(inspectSidecarRootRunner{t: t})
	_, err := b.Build(t.Context(), BuildInput{
		Layers: []io.Reader{
			gzLayer(t, []entry{{name: "removed", body: "first layer"}, {name: "opaque/lower", body: "first layer"}}),
			gzLayer(t, []entry{{name: ".wh.removed"}, {name: "opaque/.wh..wh..opq"},
				{name: "opaque/current", body: "replacement"}, {name: "upper/customer", body: "replacement"},
				{name: ".faas-app-upper-source/customer", body: "replacement"},
				{name: "bin/sidecar", body: "#!/bin/sh\n", mode: 0o755}, {name: "dev/ignored", typeflag: tar.TypeChar}}),
		},
		Manifest: api.SidecarBuildManifest(), WorkloadName: "metrics", WorkloadManifest: &manifest,
		GuestInitPath: guest, Plan: api.PlanPro, Storage: newTestStorage(t), StorageKey: "sidecars/test.ext4",
	})
	if err != nil {
		t.Fatal(err)
	}
}
