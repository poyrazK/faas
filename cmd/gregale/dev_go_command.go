package main

import (
	"fmt"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
)

// withDevGoCommand selects which `cmd/<name>` main package of the Go module
// the developer environment builds (ADR-20261026120412); empty builds what
// the source builds by default.
func withDevGoCommand(req api.UpsertDevSessionRequest, command string) api.UpsertDevSessionRequest {
	req.GoCommand = command
	return req
}

// checkDevGoCommand refuses a Go command the source cannot build before
// anything remote changes.
func checkDevGoCommand(sourceDir string, config devSourceConfig, command string) error {
	if command == "" {
		return nil
	}
	if config.shape == shapeFunction {
		return fmt.Errorf("--go-command selects a command of a Go app; %s is a function", sourceDir)
	}
	if !goMainPackageAt(filepath.Join(sourceDir, "cmd", command)) {
		return fmt.Errorf("cmd/%s in %s has no main package", command, sourceDir)
	}
	return nil
}

// errDevGoCommandUnsupported explains an API that did not store the Go
// command: its builds would compile the module's default command instead.
func errDevGoCommandUnsupported(command string) error {
	return fmt.Errorf("this Gregale API cannot build cmd/%s in a developer environment yet (it would build the module's default command); update the control plane, or run gregale dev in a module of its own", command)
}
