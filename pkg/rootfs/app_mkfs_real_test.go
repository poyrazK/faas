package rootfs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

type goLayerMkfsRunner struct{ sizes []int }

func (r *goLayerMkfsRunner) Run(ctx context.Context, argv []string) error {
	size, err := strconv.Atoi(strings.TrimSuffix(argv[len(argv)-1], "M"))
	if err != nil {
		return err
	}
	r.sizes = append(r.sizes, size)
	// Match the observed SSD filesystem layout, independently of CI defaults.
	command := append([]string{argv[0], "-b", "4096", "-i", "4096", "-J", "size=4", "-O", "has_journal"}, argv[1:]...)
	return (wire.ExecRunner{}).Run(ctx, command)
}

func TestAppMkfsRealGoLayer(t *testing.T) {
	for _, name := range []string{"mkfs.ext4", "debugfs"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skip(name + " not installed")
		}
	}
	// Actual file sizes from the failed Go/Alpine build. Nonzero content makes
	// mkfs allocate all data blocks instead of treating the fixture as sparse.
	handler := bytes.Repeat([]byte{'h'}, 1958072)
	var layer bytes.Buffer
	zw := gzip.NewWriter(&layer)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: "app/server", Mode: 0755, Size: int64(len(handler)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(handler); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	guest, runner := filepath.Join(dir, "init"), filepath.Join(dir, "runner")
	if err := os.WriteFile(guest, bytes.Repeat([]byte{'i'}, 12920256), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runner, bytes.Repeat([]byte{'r'}, 8728014), 0755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "app.ext4")
	run := &goLayerMkfsRunner{}
	result, err := NewBuilder(run).Build(t.Context(), BuildInput{Layers: []io.Reader{&layer}, Manifest: api.AppManifest{Entrypoint: []string{"/usr/local/bin/faas-runner", "--handler", "/app/handler"}}, GuestInitPath: guest, FunctionRunnerPath: runner, FunctionHandlerSourcePath: "/app/server", FunctionHandlerPath: "/app/handler", Plan: api.PlanFree, OutImage: output})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.sizes) != 2 || run.sizes[0] != 30 || result.SizeMB != 34 {
		t.Fatalf("attempts=%v result=%+v", run.sizes, result)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(result.SizeMB)*mib {
		t.Fatalf("bytes=%d size=%d", info.Size(), result.SizeMB)
	}
	extracted := filepath.Join(dir, "extracted")
	command := exec.CommandContext(t.Context(), "debugfs", "-R", "dump /upper/app/handler "+extracted, output)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("debugfs: %v: %s", err, data)
	}
	data, err := os.ReadFile(extracted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, handler) {
		t.Fatal("populated handler differs after retry")
	}
}
