package mcpmanagement

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// hostedCLIOutputLimit bounds each captured stream of a hosted CLI command.
const hostedCLIOutputLimit = 1 << 20

// hostedCLISlots bounds the hosted bridge's concurrent CLI commands.
var hostedCLISlots = make(chan struct{}, 4)

// HostedCLIRunner runs the gregale CLI at cliPath for one hosted grant
// (ADR-20261026195952): with that grant's credential, and a fresh empty
// private home and git configuration, so a command never reads the host's
// profiles, saved login or a repository's config. The environment is built
// from nothing but these values. The bridge runs the CLI rather than linking
// it, so a CLI release changes what the tools run without restarting it.
func HostedCLIRunner(cliPath, apiBase, uploadDir string) func(token string) LocalCLI {
	return func(token string) LocalCLI {
		return func(ctx context.Context, args []string, stdin []byte) (LocalCLIResult, error) {
			select {
			case hostedCLISlots <- struct{}{}:
				defer func() { <-hostedCLISlots }()
			case <-ctx.Done():
				return LocalCLIResult{}, ctx.Err()
			}
			home, err := os.MkdirTemp("", "gregale-mcp-cli-")
			if err != nil {
				return LocalCLIResult{}, err
			}
			defer func() { _ = os.RemoveAll(home) }()
			command := exec.CommandContext(ctx, cliPath, append([]string{"--non-interactive", "--json"}, args...)...)
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
			return runHostedCLI(ctx, command, stdin)
		}
	}
}

func runHostedCLI(ctx context.Context, command *exec.Cmd, stdin []byte) (LocalCLIResult, error) {
	command.Stdin = bytes.NewReader(stdin)
	stdout, stderr := &cappedBuffer{limit: hostedCLIOutputLimit}, &cappedBuffer{limit: hostedCLIOutputLimit}
	command.Stdout, command.Stderr = stdout, stderr
	err := command.Run()
	result := LocalCLIResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var exit *exec.ExitError
	if errors.As(err, &exit) && ctx.Err() == nil {
		result.ExitCode = exit.ExitCode()
		return result, nil
	}
	return result, err
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
