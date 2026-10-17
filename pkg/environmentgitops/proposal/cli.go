package proposal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/textproto"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// CLI uses the operator's existing provider login. Payloads travel through
// stdin, never shell code, command arguments, temporary files or error output.
func CLI(provider, repository string) (Client, error) {
	if (provider != "github" && provider != "gitlab") || !validRepository(repository, provider) {
		return Client{}, fmt.Errorf("unsupported provider repository")
	}
	return Client{Provider: provider, Path: repository, Call: cliCall(provider)}, nil
}

func cliCall(provider string) Call {
	return func(ctx context.Context, method, endpoint string, input, output any) error {
		name, host := "gh", "github.com"
		if provider == "gitlab" {
			name, host = "glab", "gitlab.com"
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		args := []string{"api", "--hostname", host, "--method", method, "--include", endpoint}
		var body []byte
		if input != nil {
			var err error
			body, err = json.Marshal(input)
			if err != nil {
				return fmt.Errorf("could not encode provider request")
			}
			args = append(args, "--input", "-")
		}
		cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // Fixed executable and separate arguments; no shell.
		cmd.Stdin, cmd.Stderr = bytes.NewReader(body), io.Discard
		// Disable provider debug logging and prompts, including ambient HTTP traces.
		for _, item := range os.Environ() {
			key, _, _ := strings.Cut(item, "=")
			if strings.Contains(key, "DEBUG") || key == "GH_PROMPT_DISABLED" || key == "GLAB_CHECK_UPDATE" || key == "NO_COLOR" || key == "CLICOLOR" {
				continue
			}
			cmd.Env = append(cmd.Env, item)
		}
		cmd.Env = append(cmd.Env, "GH_PROMPT_DISABLED=1", "GLAB_CHECK_UPDATE=false", "NO_COLOR=1", "CLICOLOR=0")
		var stdout boundedBuffer
		cmd.Stdout = &stdout
		runErr := cmd.Run()
		reader := bufio.NewReader(bytes.NewReader(stdout.Bytes()))
		line, err := reader.ReadString('\n')
		parts := strings.Fields(line)
		if err != nil || len(parts) < 2 || !strings.HasPrefix(parts[0], "HTTP/") {
			return fmt.Errorf("%s API unavailable; check its login, permissions and network", name)
		}
		status, err := strconv.Atoi(parts[1])
		if err != nil {
			return fmt.Errorf("invalid provider response status")
		}
		headers, err := textproto.NewReader(reader).ReadMIMEHeader()
		if err != nil {
			return fmt.Errorf("invalid provider response headers")
		}
		if runErr != nil || status < 200 || status >= 300 {
			if status == 404 {
				return ErrNotFound
			}
			return fmt.Errorf("%s API request failed (HTTP %d); provider body omitted", name, status)
		}
		// Endpoint reads use at most two diffs/reviews, or explicit tree pages.
		// A provider must not silently truncate the single-change proof.
		if strings.Contains(endpoint, "per_page=2") && headers.Get("X-Next-Page") != "" {
			return fmt.Errorf("provider returned additional proposal changes or reviews")
		}
		raw, err := io.ReadAll(reader)
		if err != nil || len(raw) == 0 || json.Unmarshal(raw, output) != nil {
			return fmt.Errorf("invalid provider JSON response")
		}
		return nil
	}
}

type boundedBuffer struct{ buffer bytes.Buffer }

func (b *boundedBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > 4*api.EnvironmentGitOpsMaxDefinitionBytes {
		return 0, fmt.Errorf("provider response exceeds manifest response bound")
	}
	return b.buffer.Write(p)
}
