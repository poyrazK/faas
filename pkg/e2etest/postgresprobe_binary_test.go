package e2etest

import (
	"bytes"
	"debug/elf"
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
