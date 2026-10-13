package rootfs

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// A directory entry replaces a symlink at its path, as containerd does, and
// never applies its mode through the link: symlink targets are guest-side
// strings that can name host directories.
func TestDirectoryEntryReplacesSymlinkWithoutFollowingIt(t *testing.T) {
	for name, linkTarget := range map[string]func(hostDir string) string{
		"absolute target": func(hostDir string) string { return hostDir },
		"relative target": func(hostDir string) string { return filepath.Join("..", filepath.Base(hostDir)) },
	} {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			hostDir := filepath.Join(parent, "host")
			staging := filepath.Join(parent, "staging")
			for _, dir := range []string{hostDir, staging} {
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			layer := gzLayer(t, []entry{
				{name: "x", typeflag: tar.TypeSymlink, linkname: linkTarget(hostDir)},
				{name: "x/", typeflag: tar.TypeDir},
				{name: "x/file", body: "image"},
			})
			if err := ApplyLayerGz(staging, layer); err != nil {
				t.Fatalf("ApplyLayerGz: %v", err)
			}
			info, err := os.Stat(hostDir)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o700 {
				t.Fatalf("host directory mode = %v, want 0700", info.Mode().Perm())
			}
			if _, err := os.Stat(filepath.Join(hostDir, "file")); !os.IsNotExist(err) {
				t.Fatalf("image file written into the host directory: %v", err)
			}
			replaced, err := os.Lstat(filepath.Join(staging, "x"))
			if err != nil || !replaced.IsDir() {
				t.Fatalf("staging x = %v, %v; want a real directory", replaced, err)
			}
		})
	}
}

func TestChmodImageDirectoryRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := chmodImageDirectory(link, 0o777); err == nil {
		t.Fatal("chmodImageDirectory followed a symlink")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("symlink target mode = %v, want 0700", info.Mode().Perm())
	}
}

func TestLayerExtractionBudgetIsSharedAcrossLayers(t *testing.T) {
	staging := t.TempDir()
	budget := newExtractionBudget(10)
	first := gzLayer(t, []entry{{name: "a", body: "123456"}})
	second := gzLayer(t, []entry{{name: "b", body: "123456"}})
	if err := applyLayerGzForApp(staging, first, budget); err != nil {
		t.Fatalf("first layer: %v", err)
	}
	err := applyLayerGzForApp(staging, second, budget)
	var budgetErr *ErrLayerExtractionExceedsBudget
	if !errors.As(err, &budgetErr) || budgetErr.UsedBytes != 6 || budgetErr.EntryBytes != 6 || budgetErr.LimitBytes != 10 {
		t.Fatalf("second layer error = %v, want an extraction budget error", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "b")); !os.IsNotExist(err) {
		t.Fatalf("over-budget file was written: %v", err)
	}
}

func TestLayerExtractionBudgetFailsClosedWithoutALimit(t *testing.T) {
	layer := gzLayer(t, []entry{{name: "a", body: "x"}})
	err := applyLayerGzForApp(t.TempDir(), layer, newExtractionBudget(0))
	var budgetErr *ErrLayerExtractionExceedsBudget
	if !errors.As(err, &budgetErr) {
		t.Fatalf("error = %v, want an extraction budget error", err)
	}
}

func TestLayerApplyErrorReportsBudgetAsAppLayerProblem(t *testing.T) {
	limits, ok := api.LimitsFor(api.PlanFree)
	if !ok {
		t.Fatal("no Free limits")
	}
	err := layerApplyError(limits, 2, &ErrLayerExtractionExceedsBudget{UsedBytes: 100, EntryBytes: 50, LimitBytes: 120})
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodeAppLayerTooBig {
		t.Fatalf("error = %v, want the app-layer-too-big Problem", err)
	}
	other := errors.New("read failed")
	if err := layerApplyError(limits, 2, other); !errors.Is(err, other) {
		t.Fatalf("error = %v, want the original error wrapped", err)
	}
}

func TestSpoolFullRootfsLayerStopsPastBudget(t *testing.T) {
	if _, _, err := spoolFullRootfsLayer(bytes.NewReader(make([]byte, 11)), 10); err == nil {
		t.Fatal("spool accepted a layer larger than the budget")
	}
	path, cleanup, err := spoolFullRootfsLayer(bytes.NewReader(make([]byte, 10)), 10)
	if err != nil {
		t.Fatalf("spool at the budget: %v", err)
	}
	defer cleanup()
	if info, err := os.Stat(path); err != nil || info.Size() != 10 {
		t.Fatalf("spooled file = %v, %v", info, err)
	}
}

// eofErrorReader returns err in place of io.EOF, the way a digest-checking
// blob reader reports a mismatch only once it reaches the end of the blob.
type eofErrorReader struct {
	r   io.Reader
	err error
}

func (e *eofErrorReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if errors.Is(err, io.EOF) {
		return n, e.err
	}
	return n, err
}

// Applying a layer reads its blob to the end, so a reader that verifies the
// blob at EOF always gets to verify it.
func TestApplyLayerGzReadsBlobToEOF(t *testing.T) {
	layer := gzLayer(t, []entry{{name: "a", body: "content"}})
	errAtEOF := errors.New("blob digest mismatch")
	err := ApplyLayerGz(t.TempDir(), &eofErrorReader{r: layer, err: errAtEOF})
	if !errors.Is(err, errAtEOF) {
		t.Fatalf("ApplyLayerGz error = %v, want the end-of-blob error", err)
	}
}

func TestApplyLayerGzToleratesBytesAfterTheGzipStream(t *testing.T) {
	layer := io.MultiReader(gzLayer(t, []entry{{name: "a", body: "content"}}), strings.NewReader("trailing bytes that are not gzip"))
	if err := ApplyLayerGz(t.TempDir(), layer); err != nil {
		t.Fatalf("ApplyLayerGz: %v", err)
	}
}
