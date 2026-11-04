package main

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

// minimalELF builds a little-endian ELF64 file whose dynamic section names
// needed as DT_NEEDED entries: enough for debug/elf.ImportedLibraries.
func minimalELF(t *testing.T, needed ...string) []byte {
	t.Helper()
	dynstr := []byte{0}
	var offsets []uint64
	for _, lib := range needed {
		offsets = append(offsets, uint64(len(dynstr)))
		dynstr = append(append(dynstr, lib...), 0)
	}
	var dynamic bytes.Buffer
	for _, off := range offsets {
		_ = binary.Write(&dynamic, binary.LittleEndian, [2]uint64{uint64(elf.DT_NEEDED), off})
	}
	_ = binary.Write(&dynamic, binary.LittleEndian, [2]uint64{uint64(elf.DT_NULL), 0})
	shstr := []byte("\x00.dynstr\x00.dynamic\x00.shstrtab\x00")

	const ehsize, shentsize = 64, 64
	dynstrOff := uint64(ehsize)
	dynamicOff := (dynstrOff + uint64(len(dynstr)) + 7) &^ 7
	shstrOff := dynamicOff + uint64(dynamic.Len())
	shOff := (shstrOff + uint64(len(shstr)) + 7) &^ 7

	var out bytes.Buffer
	ident := [16]byte{0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)}
	header := struct {
		Ident                                                [16]byte
		Type, Machine                                        uint16
		Version                                              uint32
		Entry, Phoff, Shoff                                  uint64
		Flags                                                uint32
		Ehsize, Phentsize, Phnum, Shentsize, Shnum, Shstrndx uint16
	}{ident, uint16(elf.ET_DYN), uint16(elf.EM_X86_64), uint32(elf.EV_CURRENT), 0, 0, shOff, 0, ehsize, 0, 0, shentsize, 4, 3}
	_ = binary.Write(&out, binary.LittleEndian, header)
	pad := func(to uint64) { out.Write(make([]byte, int(to)-out.Len())) }
	pad(dynstrOff)
	out.Write(dynstr)
	pad(dynamicOff)
	out.Write(dynamic.Bytes())
	pad(shstrOff)
	out.Write(shstr)
	pad(shOff)
	type shdr struct {
		Name, Type                uint32
		Flags, Addr, Offset, Size uint64
		Link, Info                uint32
		Addralign, Entsize        uint64
	}
	for _, s := range []shdr{
		{},
		{Name: 1, Type: uint32(elf.SHT_STRTAB), Offset: dynstrOff, Size: uint64(len(dynstr)), Addralign: 1},
		{Name: 9, Type: uint32(elf.SHT_DYNAMIC), Offset: dynamicOff, Size: uint64(dynamic.Len()), Link: 1, Addralign: 8, Entsize: 16},
		{Name: 18, Type: uint32(elf.SHT_STRTAB), Offset: shstrOff, Size: uint64(len(shstr)), Addralign: 1},
	} {
		_ = binary.Write(&out, binary.LittleEndian, s)
	}
	return out.Bytes()
}

func writeTree(t *testing.T, root string, files map[string][]byte) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

var testDropped = map[string]bool{"libffi.so.8": true, "libgnutls.so.30": true}

func TestElfNeededLibrariesReadsDTNeeded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(path, minimalELF(t, "libffi.so.8", "libc.so.6"), 0o755); err != nil {
		t.Fatal(err)
	}
	needed, isELF := elfNeededLibraries(path)
	if !isELF || strings.Join(needed, ",") != "libffi.so.8,libc.so.6" {
		t.Fatalf("needed=%v isELF=%v", needed, isELF)
	}
	text := filepath.Join(t.TempDir(), "script.sh")
	_ = os.WriteFile(text, []byte("#!/bin/sh\n"), 0o755)
	if _, isELF := elfNeededLibraries(text); isELF {
		t.Fatal("a shell script was read as ELF")
	}
}

func TestScanLayerTreeReportsOnlyUnshippedDroppedLibraries(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string][]byte{
		// Needs libffi from the base: a finding.
		"app/bin/tool": minimalELF(t, "libffi.so.8", "libc.so.6"),
		// Needs gnutls but ships it: not a finding.
		"app/bin/client":             minimalELF(t, "libgnutls.so.30"),
		"app/vendor/libgnutls.so.30": []byte("shipped copy"),
		// Only libraries the new base keeps.
		"app/bin/plain": minimalELF(t, "libc.so.6", "libssl.so.3"),
		"app/README":    []byte("not ELF"),
	})
	outside := t.TempDir()
	writeTree(t, outside, map[string][]byte{"evil": minimalELF(t, "libidn2.so.0")})
	if err := os.Symlink(filepath.Join(outside, "evil"), filepath.Join(root, "app/bin/link")); err != nil {
		t.Fatal(err)
	}

	elfFiles, needs, err := scanLayerTree(root, testDropped)
	if err != nil {
		t.Fatal(err)
	}
	if elfFiles != 3 {
		t.Fatalf("elfFiles = %d, want 3 (symlinks are not followed)", elfFiles)
	}
	if len(needs) != 1 || needs[0] != (layerNeed{file: "/app/bin/tool", library: "libffi.so.8"}) {
		t.Fatalf("needs = %+v", needs)
	}
}

type fakeLayerBackend struct{ layers map[string][]byte }

func (b fakeLayerBackend) Put(context.Context, string, io.Reader) error {
	return errors.New("read-only")
}
func (b fakeLayerBackend) Delete(context.Context, string) error { return errors.New("read-only") }
func (b fakeLayerBackend) Get(_ context.Context, key string) (io.ReadCloser, error) {
	body, ok := b.layers[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

// fakeDump "dumps" a layer whose bytes name the tree to write.
func fakeDump(trees map[string]map[string][]byte) func(context.Context, string, string, string) error {
	return func(_ context.Context, _, image, dir string) error {
		body, err := os.ReadFile(image)
		if err != nil {
			return err
		}
		tree, ok := trees[string(body)]
		if !ok {
			return errors.New("debugfs could not dump the layer: bad magic")
		}
		for name, content := range tree {
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, content, 0o644); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestAuditBaseLayersReportsFindingsAndUnauditedLayers(t *testing.T) {
	backend := fakeLayerBackend{layers: map[string][]byte{
		"apps/web/d1.ext4":        []byte("needs-ffi"),
		"apps/web/d1-worker.ext4": []byte("clean"),
		"apps/api/d2.ext4":        []byte("corrupt"),
	}}
	restore := baseAuditDump
	baseAuditDump = fakeDump(map[string]map[string][]byte{
		"needs-ffi": {"lost+found/.keep": nil, "app/server": minimalELF(t, "libffi.so.8")},
		"clean":     {"lost+found/.keep": nil, "app/worker": minimalELF(t, "libc.so.6")},
	})
	defer func() { baseAuditDump = restore }()

	targets := []baseAuditTarget{
		{App: "web", DeploymentID: "d1", Status: "live", Layer: "main", LayerKey: "apps/web/d1.ext4"},
		{App: "web", DeploymentID: "d1", Status: "live", Layer: "worker", LayerKey: "apps/web/d1-worker.ext4"},
		{App: "api", DeploymentID: "d2", Status: "live", Layer: "main", LayerKey: "apps/api/d2.ext4"},
		{App: "old", DeploymentID: "d3", Status: "live", Layer: "main", LayerKey: "layers/d3.ext4"},
	}
	report := auditBaseLayers(context.Background(), backend, "debugfs", t.TempDir(), targets, testDropped)

	if report.LayersAudited != 2 || report.ELFFiles != 2 {
		t.Fatalf("audited=%d elf=%d, want 2 and 2", report.LayersAudited, report.ELFFiles)
	}
	if len(report.Findings) != 1 || report.Findings[0].App != "web" || report.Findings[0].File != "/app/server" || report.Findings[0].Library != "libffi.so.8" {
		t.Fatalf("findings = %+v", report.Findings)
	}
	if len(report.Unaudited) != 2 {
		t.Fatalf("unaudited = %+v, want the corrupt layer and the missing one", report.Unaudited)
	}
	for _, gap := range report.Unaudited {
		if gap.Reason == "" {
			t.Fatalf("gap without a reason: %+v", gap)
		}
	}
}

func TestCmdAuditBaseLibrariesExitCodesAndJSON(t *testing.T) {
	restoreList, restoreBackend, restoreDump, restoreJSON := baseAuditListLayers, baseAuditBackend, baseAuditDump, jsonOutput
	defer func() {
		baseAuditListLayers, baseAuditBackend, baseAuditDump, jsonOutput = restoreList, restoreBackend, restoreDump, restoreJSON
	}()
	var gotStatuses []string
	var gotApp string
	layers := map[string][]byte{"apps/web/d1.ext4": []byte("needs-ffi")}
	baseAuditListLayers = func(_, _ string, statuses []string, app string) ([]baseAuditTarget, error) {
		gotStatuses, gotApp = statuses, app
		return []baseAuditTarget{{App: "web", DeploymentID: "d1", Status: "live", Layer: "main", LayerKey: "apps/web/d1.ext4"}}, nil
	}
	baseAuditBackend = func(context.Context) (storage.StorageBackend, error) { return fakeLayerBackend{layers: layers}, nil }
	baseAuditDump = fakeDump(map[string]map[string][]byte{
		"needs-ffi": {"app/server": minimalELF(t, "libffi.so.8")},
		"clean":     {"app/server": minimalELF(t, "libc.so.6")},
	})
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to stand in for debugfs")
	}

	var stdout bytes.Buffer
	restoreOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = restoreOut }()

	jsonOutput = true
	if code := cmdAuditDispatch([]string{"base-libraries", "--debugfs", sh, "--app", "web", "--include-superseded", "--work-dir", t.TempDir()}); code != 1 {
		t.Fatalf("exit = %d, want 1 for a finding", code)
	}
	if gotApp != "web" || gotStatuses[len(gotStatuses)-1] != "superseded" {
		t.Fatalf("listing args: app=%q statuses=%v", gotApp, gotStatuses)
	}
	var report baseAuditReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("JSON output: %v\n%s", err, stdout.String())
	}
	if len(report.Findings) != 1 || len(report.Libraries) != len(debian13DroppedLibraries) {
		t.Fatalf("report = %+v", report)
	}

	layers["apps/web/d1.ext4"] = []byte("clean")
	jsonOutput = false
	stdout.Reset()
	if code := cmdAuditDispatch([]string{"base-libraries", "--debugfs", sh, "--work-dir", t.TempDir()}); code != 0 {
		t.Fatalf("exit = %d, want 0 when clean\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "OK: no audited layer needs") {
		t.Fatalf("text output = %q", stdout.String())
	}
	if gotStatuses[len(gotStatuses)-1] != "live" {
		t.Fatalf("superseded deployments audited by default: %v", gotStatuses)
	}
}

func TestCmdAuditBaseLibrariesRejectsBadUsage(t *testing.T) {
	for _, args := range [][]string{
		{"base-libraries", "extra"},
		{"base-libraries", "--libraries", " , "},
		{"base-libraries", "--work-dir", "/tmp/has space"},
	} {
		if code := cmdAuditDispatch(args); code != 2 {
			t.Fatalf("%v: exit = %d, want 2", args, code)
		}
	}
}

// TestDumpExt4WithDebugfsEndToEnd builds a real ext4 layer and audits it with
// the real debugfs. It needs e2fsprogs (standard on Linux CI runners).
func TestDumpExt4WithDebugfsEndToEnd(t *testing.T) {
	mke2fs, err := exec.LookPath("mke2fs")
	if err != nil {
		t.Skip("mke2fs not installed")
	}
	debugfs, err := exec.LookPath("debugfs")
	if err != nil {
		t.Skip("debugfs not installed")
	}
	src := t.TempDir()
	writeTree(t, src, map[string][]byte{
		"app/bin/tool":   minimalELF(t, "libunistring.so.2"),
		"app/bin/client": minimalELF(t, "libc.so.6"),
	})
	image := filepath.Join(t.TempDir(), "layer.ext4")
	if out, err := exec.Command(mke2fs, "-q", "-F", "-t", "ext4", "-d", src, image, "8M").CombinedOutput(); err != nil {
		t.Skipf("mke2fs -d unsupported here: %v %s", err, out)
	}
	tree := t.TempDir()
	if err := dumpExt4WithDebugfs(context.Background(), debugfs, image, tree); err != nil {
		t.Fatal(err)
	}
	elfFiles, needs, err := scanLayerTree(tree, map[string]bool{"libunistring.so.2": true})
	if err != nil {
		t.Fatal(err)
	}
	if elfFiles != 2 || len(needs) != 1 || needs[0].file != "/app/bin/tool" {
		t.Fatalf("elfFiles=%d needs=%+v", elfFiles, needs)
	}

	notExt4 := filepath.Join(t.TempDir(), "junk.ext4")
	_ = os.WriteFile(notExt4, []byte("not a filesystem"), 0o600)
	if err := dumpExt4WithDebugfs(context.Background(), debugfs, notExt4, t.TempDir()); err == nil {
		t.Fatal("a non-ext4 file dumped without error")
	}
}
