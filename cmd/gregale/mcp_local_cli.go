package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
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
		command := exec.CommandContext(ctx, executable, append(global, args...)...)
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
