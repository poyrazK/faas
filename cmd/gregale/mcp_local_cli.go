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

// mcpCLIExecutable is the binary the MCP tools run: this one.
var mcpCLIExecutable = os.Executable

// mcpLocalCLI runs this binary for the management MCP server's local deploy
// and env tools (ADR-20261026141841). Each command is non-interactive JSON,
// acts as the MCP connection's credential and endpoint, and writes to bounded
// buffers — never to the MCP protocol's stdout.
func mcpLocalCLI(profile, apiBase, token string) mcpmanagement.LocalCLI {
	return func(ctx context.Context, args []string, stdin []byte) (mcpmanagement.LocalCLIResult, error) {
		executable, err := mcpCLIExecutable()
		if err != nil {
			return mcpmanagement.LocalCLIResult{}, err
		}
		global := []string{"--non-interactive", "--json"}
		if profile != "" {
			global = append(global, "--profile", profile)
		}
		command := exec.CommandContext(ctx, executable, append(global, args...)...)
		// A neutral directory: a linked project context where the server
		// started must not redirect a command's target. Tools pass paths and
		// apps explicitly.
		command.Dir = os.TempDir()
		command.Env = append(environWithout(os.Environ(), "FAAS_TOKEN", "FAAS_API", "FAAS_JSON"), "FAAS_TOKEN="+token, "FAAS_API="+apiBase)
		return runMCPCLI(ctx, command, stdin)
	}
}

// mcpHostedCLISlots bounds the hosted bridge's concurrent CLI commands.
var mcpHostedCLISlots = make(chan struct{}, 4)

// mcpHostedCLI runs this binary as one hosted grant (ADR-20261026195952): its
// own credential, and a fresh empty private home and git configuration, so a
// command never reads the host's profiles, saved login or a repository's
// config. The environment is built from nothing but these values.
func mcpHostedCLI(apiBase, uploadDir string) func(token string) mcpmanagement.LocalCLI {
	return func(token string) mcpmanagement.LocalCLI {
		return func(ctx context.Context, args []string, stdin []byte) (mcpmanagement.LocalCLIResult, error) {
			select {
			case mcpHostedCLISlots <- struct{}{}:
				defer func() { <-mcpHostedCLISlots }()
			case <-ctx.Done():
				return mcpmanagement.LocalCLIResult{}, ctx.Err()
			}
			executable, err := mcpCLIExecutable()
			if err != nil {
				return mcpmanagement.LocalCLIResult{}, err
			}
			home, err := os.MkdirTemp("", "gregale-mcp-cli-")
			if err != nil {
				return mcpmanagement.LocalCLIResult{}, err
			}
			defer func() { _ = os.RemoveAll(home) }()
			command := exec.CommandContext(ctx, executable, append([]string{"--non-interactive", "--json"}, args...)...)
			command.Dir = home
			command.Env = []string{
				"PATH=/usr/bin:/bin",
				"HOME=" + home,
				"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
				"TMPDIR=" + home,
				"GIT_CONFIG_NOSYSTEM=1",
				"GIT_CONFIG_GLOBAL=" + os.DevNull,
				// Git never looks above an upload for a repository.
				"GIT_CEILING_DIRECTORIES=" + home + ":" + uploadDir,
				"FAAS_TOKEN=" + token,
				"FAAS_API=" + apiBase,
			}
			return runMCPCLI(ctx, command, stdin)
		}
	}
}

func runMCPCLI(ctx context.Context, command *exec.Cmd, stdin []byte) (mcpmanagement.LocalCLIResult, error) {
	command.Stdin = bytes.NewReader(stdin)
	stdout, stderr := &cappedBuffer{limit: mcpLocalCLIOutputLimit}, &cappedBuffer{limit: mcpLocalCLIOutputLimit}
	command.Stdout, command.Stderr = stdout, stderr
	err := command.Run()
	result := mcpmanagement.LocalCLIResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var exit *exec.ExitError
	if errors.As(err, &exit) && ctx.Err() == nil {
		result.ExitCode = exit.ExitCode()
		return result, nil
	}
	return result, err
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
