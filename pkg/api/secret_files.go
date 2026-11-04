package api

import (
	"fmt"
	"regexp"
	"slices"
)

// SecretFilesDir is where guest-init writes a workload's secret files, the
// directory Compose mounts service secrets in.
const SecretFilesDir = "/run/secrets"

// SecretFileDefaultMode is Compose's default secret file mode.
const SecretFileDefaultMode = 0o444

// SecretFile writes the app secret Secret to SecretFilesDir/Name in the main
// workload (ADR-20261023090000), as Compose mounts a service's secrets. It
// names the secret; its value is never stored here.
type SecretFile struct {
	Secret string `json:"secret"`
	Name   string `json:"name"`
	// Mode is the file's permission bits; zero means SecretFileDefaultMode.
	Mode uint32 `json:"mode,omitempty"`
}

// EffectiveMode returns the permission bits guest-init applies.
func (f SecretFile) EffectiveMode() uint32 {
	if f.Mode == 0 {
		return SecretFileDefaultMode
	}
	return f.Mode
}

// A file name under SecretFilesDir. Starting with a letter or digit rules out
// "." and "..", and there is no '/' to leave the directory.
var secretFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// ValidateSecretFiles checks names, modes and the count against
// pkg/api/limits.go.
func ValidateSecretFiles(files []SecretFile) error {
	if len(files) > SecretFilesMax {
		return fmt.Errorf("secrets has %d files; the limit is %d", len(files), SecretFilesMax)
	}
	names := make(map[string]struct{}, len(files))
	for _, file := range files {
		if !secretFileName.MatchString(file.Name) {
			return fmt.Errorf("secret file name %q must be letters, digits, '.', '_' or '-', at most 128, starting with a letter or digit", file.Name)
		}
		if _, duplicate := names[file.Name]; duplicate {
			return fmt.Errorf("secret file %q is declared twice", file.Name)
		}
		names[file.Name] = struct{}{}
		if problem := ValidateSecretKey(file.Secret); problem != nil {
			return fmt.Errorf("secret file %q names app secret %q: %s", file.Name, file.Secret, problem.Detail)
		}
		if file.Mode > 0o777 {
			return fmt.Errorf("secret file %q mode %#o must be permission bits (at most 0777)", file.Name, file.Mode)
		}
	}
	return nil
}

// CloneSecretFiles keeps nil distinct from empty.
func CloneSecretFiles(files []SecretFile) []SecretFile {
	return slices.Clone(files)
}
