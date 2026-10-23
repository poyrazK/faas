package rootfs

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
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

// The byte budget counts only file content, so empty files, directories and
// links are free. A small compressed layer of empty entries must still stop
// at the entry cap instead of creating millions of inodes on the node's image
// staging filesystem.
func TestLayerExtractionEntryCapIsSharedAcrossLayers(t *testing.T) {
	staging := t.TempDir()
	budget := newExtractionBudget(1 << 20)
	budget.maxEntries = 3
	if err := applyLayerGzForApp(staging, gzLayer(t, []entry{{name: "a"}, {name: "b"}}), budget); err != nil {
		t.Fatalf("first layer: %v", err)
	}
	err := applyLayerGzForApp(staging, gzLayer(t, []entry{{name: "c"}, {name: "d"}}), budget)
	var entriesErr *ErrLayerExtractionTooManyEntries
	if !errors.As(err, &entriesErr) || entriesErr.LimitEntries != 3 {
		t.Fatalf("second layer error = %v, want an entry cap error", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "d")); !os.IsNotExist(err) {
		t.Fatalf("over-cap entry was written: %v", err)
	}
}

func TestLayerExtractionEntryCapDefaultsToTheScanBound(t *testing.T) {
	if got, want := newExtractionBudget(1).maxEntries, api.ApplicationStandardRuntimeScanMaxEntries*api.LayerExtractionBudgetMultiple; got != want || got != api.LayerExtractionMaxEntries {
		t.Fatalf("maxEntries = %d, want %d (api.LayerExtractionMaxEntries %d)", got, want, api.LayerExtractionMaxEntries)
	}
}

func TestLayerApplyErrorReportsEntryCapAsAppLayerProblem(t *testing.T) {
	limits, ok := api.LimitsFor(api.PlanFree)
	if !ok {
		t.Fatal("no Free limits")
	}
	err := layerApplyError(limits, 1, &ErrLayerExtractionTooManyEntries{LimitEntries: 2_000_000})
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodeAppLayerTooBig || !strings.Contains(problem.Detail, "2000000") {
		t.Fatalf("error = %v, want the app-layer-too-big Problem naming the entry limit", err)
	}
}

// An entry naming the staging root (".", "./.", "a/..") may only be a
// directory. A symlink there replaced the empty root with a link, and every
// later entry was written wherever it pointed: with imaged's staging under
// /srv/fc/base, "./ -> .." reached the shared base images.
func TestLayerEntryCannotReplaceStagingRoot(t *testing.T) {
	for _, rootName := range []string{".", "./.", "a/.."} {
		for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeReg} {
			t.Run(fmt.Sprintf("%q/%c", rootName, kind), func(t *testing.T) {
				root := t.TempDir()
				outside := filepath.Join(root, "outside")
				staging := filepath.Join(root, "staging")
				for _, dir := range []string{outside, staging} {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				layer := gzLayer(t, []entry{
					{name: rootName, typeflag: kind, linkname: "../outside"},
					{name: "./planted", body: "x"},
				})
				err := applyLayerGzForApp(staging, layer, newExtractionBudget(1<<20))
				if err == nil {
					t.Fatal("a non-directory entry at the image root was accepted")
				}
				if info, statErr := os.Lstat(staging); statErr != nil || !info.IsDir() {
					t.Fatalf("staging root replaced: %v %v", info, statErr)
				}
				if _, statErr := os.Stat(filepath.Join(outside, "planted")); !os.IsNotExist(statErr) {
					t.Fatalf("entry written outside staging: %v", statErr)
				}
			})
		}
	}
	// A directory entry for the root stays valid: real layers carry "./".
	if err := applyLayerGzForApp(t.TempDir(), gzLayer(t, []entry{{name: "./", typeflag: tar.TypeDir}, {name: "./f", body: "x"}}), newExtractionBudget(1<<20)); err != nil {
		t.Fatalf("root directory entry rejected: %v", err)
	}
}

// Function source tarballs share applyEntry. A symlink named "." must not
// replace the /app directory and redirect the rest of the archive.
func TestFunctionTarballEntryCannotReplaceAppRoot(t *testing.T) {
	root := t.TempDir()
	staging := filepath.Join(root, "staging")
	if err := os.MkdirAll(filepath.Join(staging, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, gzLayer(t, []entry{
		{name: ".", typeflag: tar.TypeSymlink, linkname: "../.."},
		{name: "planted", body: "x"},
	})); err != nil {
		t.Fatal(err)
	}
	if err := applyTarballWithCap(filepath.Join(staging, "app"), &buf, 1<<20, ""); err == nil {
		t.Fatal("a symlink at the function source root was accepted")
	}
	if info, err := os.Lstat(filepath.Join(staging, "app")); err != nil || !info.IsDir() {
		t.Fatalf("app root replaced: %v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(root, "planted")); !os.IsNotExist(err) {
		t.Fatalf("entry written outside staging: %v", err)
	}
}
