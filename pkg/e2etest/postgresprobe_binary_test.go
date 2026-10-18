package e2etest

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPostgresProbeExecutableIsStaticGuestBinary(t *testing.T) {
	binary, err := PostgresProbeExecutable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := elf.NewFile(bytes.NewReader(binary))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = image.Close() }()
	if image.Machine != elf.EM_X86_64 || image.Class != elf.ELFCLASS64 {
		t.Fatal("probe does not target the supported x86_64 guest")
	}
	for _, program := range image.Progs {
		if program.Type == elf.PT_INTERP {
			t.Fatal("scratch fixture requires a dynamic loader")
		}
	}
}

func TestPostgresProbeBuildDoesNotImportControlPlane(t *testing.T) {
	// ADR-465: this customer SQL workload runs independently of the catalog.
	// Importing the control plane also forces a cold static build of its large
	// state packages inside the fixture's bounded build deadline.
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate PostgreSQL probe source")
	}
	command := exec.Command("go", "list", "-deps", ".")
	command.Dir = filepath.Join(filepath.Dir(source), "testdata", "postgresprobe")
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("list probe dependencies: %v\n%s", err, output)
	}
	for _, dependency := range strings.Fields(string(output)) {
		for _, forbidden := range []string{"pkg/state", "pkg/apid", "pkg/managedpostgres"} {
			name := "github.com/onebox-faas/faas/" + forbidden
			if dependency == name || forbidden != "pkg/managedpostgres" && strings.HasPrefix(dependency, name+"/") {
				t.Errorf("guest probe imports control plane package %s", dependency)
			}
		}
	}
}
