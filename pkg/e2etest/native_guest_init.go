package e2etest

import (
	"debug/buildinfo"
	"debug/elf"
	"fmt"
	"os"
)

// ValidateNativeGuestInit prevents the acceptance harness from replacing PID 1
// with its non-guest placeholder or an incompatible executable.
func ValidateNativeGuestInit(path string) error {
	invalid := fmt.Errorf("native acceptance requires the static Linux amd64 guest/init executable compiled from this checkout")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return invalid
	}
	image, err := elf.Open(path)
	if err != nil {
		return invalid
	}
	defer func() { _ = image.Close() }()
	if image.Machine != elf.EM_X86_64 || image.Class != elf.ELFCLASS64 {
		return invalid
	}
	for _, program := range image.Progs {
		if program.Type == elf.PT_INTERP {
			return invalid
		}
	}
	build, err := buildinfo.ReadFile(path)
	if err != nil || build.Path != "github.com/onebox-faas/faas/guest/init" {
		return invalid
	}
	return nil
}
