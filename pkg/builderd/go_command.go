package builderd

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// selectGoCommand returns the `cmd/<name>` main package a Railpack build of
// a multi-command Go module compiles for app (ADR-20261025134512), or "".
// A selection the build cannot honour is an error rather than a build of
// whatever the source would produce by default: a Dockerfile or a source
// that is not a Go module would give every command of the module one image.
func selectGoCommand(app state.App, dockerfilePath string, fw Framework) (string, error) {
	command := app.Manifest.BuildGoCommand
	switch {
	case command == "":
		return "", nil
	case !api.ValidGoCommand(command):
		return "", fmt.Errorf("go command %q is not a cmd/ directory name", command)
	case dockerfilePath != "" || fw != FrameworkGo:
		return "", fmt.Errorf("this service builds cmd/%s of a Go module, but its source builds as %s", command, fw)
	}
	return command, nil
}
