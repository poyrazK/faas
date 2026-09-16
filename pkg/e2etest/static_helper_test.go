package e2etest

// Tests for StaticHelperBinaries — the binaries the harness must build with
// CGO_ENABLED=0 because they execute inside a jailer chroot.
//
// The regression: JailerVMM copies vmmd-jail-helper into each instance root as
// /faas-mount-helper and runs it under nsenter to build the jail's private
// device tree. resolveMountHelper looks for it as a sibling of the running
// vmmd and falls back to the vmmd binary when it is absent. The harness never
// built it, so the fallback applied — and the fallback cannot work under the
// metal suite, which runs with -race and therefore cgo: the vmmd binary is
// dynamically linked, and execve of a dynamic binary inside a chroot with no
// loader fails with ENOENT. On hardware that read as
//
//	prepare jail device tree: exit status 127
//	  (nsenter: failed to execute /faas-mount-helper: No such file or directory)
//
// and failed every builder cold boot.

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStaticHelperBinaries_NameRealMainPackages(t *testing.T) {
	if len(StaticHelperBinaries) == 0 {
		t.Fatal("StaticHelperBinaries is empty; vmmd-jail-helper must be built for jailed cold boots")
	}
	mod, err := moduleImportPath()
	if err != nil {
		t.Fatalf("module import path: %v", err)
	}
	for _, h := range StaticHelperBinaries {
		out, err := exec.Command("go", "list", "-f", "{{.Name}}", mod+"/cmd/"+h).CombinedOutput()
		if err != nil {
			t.Errorf("StaticHelperBinaries names %q but cmd/%s does not resolve: %v\n%s",
				h, h, err, out)
			continue
		}
		if got := strings.TrimSpace(string(out)); got != "main" {
			t.Errorf("cmd/%s is package %q, want main", h, got)
		}
	}
}

// resolveMountHelper finds the helper by name as a sibling of vmmd, so the
// name is a contract with pkg/fcvm, not an arbitrary label.
func TestStaticHelperBinaries_IncludesTheJailMountHelper(t *testing.T) {
	for _, h := range StaticHelperBinaries {
		if h == "vmmd-jail-helper" {
			return
		}
	}
	t.Errorf("StaticHelperBinaries = %v, missing vmmd-jail-helper; without it "+
		"resolveMountHelper falls back to the -race-built vmmd binary, which "+
		"cannot execve inside the jail chroot", StaticHelperBinaries)
}

// The property that actually matters: no PT_INTERP, i.e. no dynamic loader is
// required. A chroot built by jailer contains no loader and no libc, so a
// binary with an interpreter cannot start there at all.
func TestStaticHelperBinaries_LinkStatically(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF interpreter check is Linux-only; the jail chroot is a Linux construct")
	}
	mod, err := moduleImportPath()
	if err != nil {
		t.Fatalf("module import path: %v", err)
	}
	dir := t.TempDir()

	for _, h := range StaticHelperBinaries {
		t.Run(h, func(t *testing.T) {
			out := filepath.Join(dir, h)
			cmd := exec.Command("go", "build", "-o", out, mod+"/cmd/"+h)
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
			if combined, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("go build %s: %v\n%s", h, err, combined)
			}

			f, err := elf.Open(out)
			if err != nil {
				t.Fatalf("open %s as ELF: %v", h, err)
			}
			defer func() { _ = f.Close() }()

			for _, prog := range f.Progs {
				if prog.Type == elf.PT_INTERP {
					t.Fatalf("%s has a PT_INTERP segment, so it needs a dynamic "+
						"loader; it cannot execve inside the jailer chroot", h)
				}
			}
		})
	}
}
