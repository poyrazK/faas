package apihostingcontract

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/onebox-faas/faas/pkg/oci"
)

// ValidateContainerContract projects the fixture's image metadata through
// the same OCI manifest path used by imaged. This keeps the metal-free catalog
// gate honest about the executable process contract while lifecycle behaviour
// remains covered by the reference-node suite.
func ValidateContainerContract(fixture Fixture) error {
	if !hasTag(fixture.Tags, "oci") {
		if fixture.Container != nil {
			return fmt.Errorf("non-OCI fixture must not declare a container contract")
		}
		return nil
	}
	if fixture.Container == nil {
		return fmt.Errorf("OCI fixture must declare a container contract")
	}
	contract := fixture.Container
	if !contract.Stateless || !contract.ScaleToZero || !contract.RequestWake {
		return fmt.Errorf("container lifecycle contract must be stateless, scale-to-zero, and request-wake enabled")
	}
	if len(contract.Entrypoint) == 0 && len(contract.Cmd) == 0 {
		return fmt.Errorf("container contract must declare an entrypoint or command")
	}

	exposed := make(map[string]struct{}, len(contract.ExposedPorts))
	for _, port := range contract.ExposedPorts {
		if _, exists := exposed[port]; exists {
			return fmt.Errorf("container contract repeats exposed port %q", port)
		}
		exposed[port] = struct{}{}
	}
	manifest, err := oci.ManifestFromConfig(oci.Config{
		Entrypoint:   contract.Entrypoint,
		Cmd:          contract.Cmd,
		Env:          contract.Env,
		WorkingDir:   contract.WorkingDir,
		User:         contract.User,
		ExposedPorts: exposed,
	})
	if err != nil {
		return fmt.Errorf("derive OCI manifest: %w", err)
	}
	wantArgv := append(append([]string{}, contract.Entrypoint...), contract.Cmd...)
	if !reflect.DeepEqual(manifest.Entrypoint, wantArgv) {
		return fmt.Errorf("argv = %v, want %v", manifest.Entrypoint, wantArgv)
	}
	if !reflect.DeepEqual(manifest.Env, contract.Env) {
		return fmt.Errorf("env = %v, want %v", manifest.Env, contract.Env)
	}
	if manifest.WorkingDir != contract.WorkingDir || manifest.User != contract.User {
		return fmt.Errorf("process fields = (%q, %q), want (%q, %q)", manifest.WorkingDir, manifest.User, contract.WorkingDir, contract.User)
	}
	if manifest.Port != fixture.Expected.Port {
		return fmt.Errorf("serving port = %d, want fixture port %d", manifest.Port, fixture.Expected.Port)
	}
	command := strings.Join(wantArgv, "\x00")
	if contract.HonorsPort && !strings.Contains(command, "$PORT") && !strings.Contains(command, "${PORT") {
		return fmt.Errorf("container marked honors_port but command does not reference $PORT")
	}
	return nil
}

func hasTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}
