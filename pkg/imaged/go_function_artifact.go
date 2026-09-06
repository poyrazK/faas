package imaged

import (
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/oci"
)

// Use the executable selected by the builder export. Railpack 0.38 emits
// /app/out, while older exports used /app/server. Never execute the image's
// shell command on the host; only a single literal executable path is accepted.
func goFunctionExecutable(config oci.Config) (string, error) {
	program := ""
	if len(config.Entrypoint) == 2 && (config.Entrypoint[0] == "/bin/bash" || config.Entrypoint[0] == "/bin/sh") && config.Entrypoint[1] == "-c" {
		if len(config.Cmd) != 1 {
			return "", fmt.Errorf("go function build command must name one executable")
		}
		program = config.Cmd[0]
	} else if len(config.Entrypoint) > 0 {
		program = config.Entrypoint[0]
	} else if len(config.Cmd) > 0 {
		program = config.Cmd[0]
	} else {
		return "/app/server", nil // Legacy exports without process metadata.
	}
	if program == "" || strings.IndexFunc(program, func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return false
		default:
			return !strings.ContainsRune("/._-", r)
		}
	}) >= 0 {
		return "", fmt.Errorf("go function build command must be a literal executable path")
	}
	workingDir := config.WorkingDir
	if workingDir == "" {
		workingDir = "/app"
	}
	if !path.IsAbs(program) {
		program = path.Join(workingDir, program)
	}
	program = path.Clean(program)
	if !strings.HasPrefix(program, "/app/") {
		return "", fmt.Errorf("go function executable must be inside /app: %q", program)
	}
	return program, nil
}

// Root.Open keeps image symlinks inside the extracted tree. Validate and copy
// the same descriptor so neither command metadata nor symlinks can select a
// host file while normalizing the compiled function.
func openGoFunctionExecutable(staging, source string) (*os.File, os.FileInfo, error) {
	root, err := os.OpenRoot(staging)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(strings.TrimPrefix(source, "/"))
	if err != nil {
		return nil, nil, fmt.Errorf("open Go build executable %s: %w", source, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		_ = file.Close()
		return nil, nil, fmt.Errorf("go build executable %s is not an executable regular file", source)
	}
	return file, info, nil
}
