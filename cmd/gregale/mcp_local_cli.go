package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/mcpmanagement"
)

// mcpLocalCLIOutputLimit bounds each captured stream of a local tool command.
const mcpLocalCLIOutputLimit = 1 << 20

// mcpLocalCLI runs this binary for the management MCP server's local deploy
// and env tools (ADR-20261026141841). Each command is non-interactive JSON,
// acts as the MCP connection's credential and endpoint, and writes to bounded
// buffers — never to the MCP protocol's stdout.
func mcpLocalCLI(profile, apiBase, token string) mcpmanagement.LocalCLI {
	return func(ctx context.Context, args []string, stdin []byte) (mcpmanagement.LocalCLIResult, error) {
		executable, err := os.Executable()
		if err != nil {
			return mcpmanagement.LocalCLIResult{}, err
		}
		global := []string{"--non-interactive", "--json"}
		if profile != "" {
			global = append(global, "--profile", profile)
		}
		dir, err := mcpLocalCLIDir()
		if err != nil {
			return mcpmanagement.LocalCLIResult{}, err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		command := exec.CommandContext(ctx, executable, append(global, args...)...)
		// A neutral directory: a linked project context where the server
		// started must not redirect a command's target. Tools pass paths and
		// apps explicitly.
		command.Dir = dir
		command.Env = append(environWithout(os.Environ(), "FAAS_TOKEN", "FAAS_API", "FAAS_JSON"), "FAAS_TOKEN="+token, "FAAS_API="+apiBase)
		command.Stdin = bytes.NewReader(stdin)
		stdout, stderr := &cappedBuffer{limit: mcpLocalCLIOutputLimit}, &cappedBuffer{limit: mcpLocalCLIOutputLimit}
		command.Stdout, command.Stderr = stdout, stderr
		err = command.Run()
		result := mcpmanagement.LocalCLIResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
		var exit *exec.ExitError
		if errors.As(err, &exit) && ctx.Err() == nil {
			result.ExitCode = exit.ExitCode()
			return result, nil
		}
		return result, err
	}
}

// mcpLocalCLIDir makes a fresh working directory for one command under the
// user's cache directory. The CLI reads the nearest .gregale/project.json
// above its working directory, so no ancestor may be writable by another
// user: in a shared /tmp anyone could plant one and break or redirect the
// tools.
func mcpLocalCLIDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	parent := filepath.Join(base, "gregale", "mcp-cli")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, "run-")
}

func environWithout(environ []string, names ...string) []string {
	out := make([]string, 0, len(environ))
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		drop := false
		for _, excluded := range names {
			drop = drop || name == excluded
		}
		if !drop {
			out = append(out, entry)
		}
	}
	return out
}

// cappedBuffer keeps the first limit bytes and discards the rest, so a noisy
// command cannot grow memory or block on a full pipe.
type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
