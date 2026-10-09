package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/term"
)

// The terminal owns line editing and echo for the whole collection session.
// Mixing a buffered name reader with fd-based password reads can read ahead
// into a value, so all inputs use the same terminal reader instead.
type secretTerminalIO struct {
	input  io.Reader
	output io.Writer
}

func (s secretTerminalIO) Read(p []byte) (int, error) {
	// Do not read ahead from a name into a following hidden value.
	if len(p) > 1 {
		p = p[:1]
	}
	n, err := s.input.Read(p)
	for _, b := range p[:n] {
		if b == 3 { // Ctrl-C in raw mode.
			return 0, context.Canceled
		}
		if b == 4 { // Ctrl-D closes the session without accepting a default.
			return 0, io.EOF
		}
	}
	return n, err
}

func (s secretTerminalIO) Write(p []byte) (int, error) { return s.output.Write(p) }

type secretEntryTerminal struct {
	*term.Terminal
	input    io.Reader
	maxBytes int
}

// x/term's editable lines silently cap at 4096 runes. Read hidden values
// directly from the raw terminal so larger plan-supported secrets cannot
// be silently truncated. Only names and non-secret prompts use ReadLine.
func (t *secretEntryTerminal) ReadPassword(label string) (string, error) {
	_, _ = fmt.Fprint(t.Terminal, label)
	defer fmt.Fprintln(t.Terminal)
	value := make([]byte, 0, min(t.maxBytes, 4096))
	overLimit := false
	for {
		var b [1]byte
		if _, err := io.ReadFull(t.input, b[:]); err != nil {
			return "", err
		}
		switch b[0] {
		case '\r', '\n':
			if overLimit {
				return "", fmt.Errorf("value exceeds the plan's %d-byte limit; no secrets were saved", t.maxBytes)
			}
			return string(value), nil
		case 21: // Ctrl-U clears the hidden line.
			value = value[:0]
			overLimit = false
		case 127, 8:
			if len(value) > 0 {
				_, size := utf8.DecodeLastRune(value)
				value = value[:len(value)-size]
			}
		default:
			if overLimit || len(value) >= t.maxBytes {
				// Consume the rest of the hidden line before restoring echo.
				overLimit = true
				continue
			}
			value = append(value, b[0])
		}
	}
}

func secretTerminalRead(ctx context.Context, terminal *secretEntryTerminal, label string, hidden bool) (string, error) {
	type answer struct {
		value string
		err   error
	}
	result := make(chan answer, 1)
	go func() {
		var value string
		var err error
		if hidden {
			value, err = terminal.ReadPassword(label + ": ")
		} else {
			terminal.SetPrompt(label + ": ")
			value, err = terminal.ReadLine()
		}
		result <- answer{value, err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case answer := <-result:
		return answer.value, answer.err
	}
}

func secretsSetInteractive(app, scope, class string) int {
	input, ok := osStdin.(*os.File)
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() || !ok || !term.IsTerminal(int(input.Fd())) {
		return printErr("Interactive terminal required", errors.New("--interactive requires terminal input and output; use --from-stdin for scripts"))
	}
	if !api.ValidAppSlug(app) {
		return printErr("Invalid app", errors.New("pass a valid app slug with --app"))
	}
	resolvedScope, err := resolveEnvironmentFlagOrContext(scope)
	if err != nil {
		return printErr("Could not read local project context", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	account, err := client.Whoami(context.Background())
	if err != nil {
		return printErr("Could not read account plan", err)
	}
	limits, ok := api.LimitsFor(api.Plan(account.Plan))
	if !ok {
		return printErr("Unknown account plan", errors.New("use --from-stdin to supply secrets explicitly"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pairs, selectedScope, selectedClass, confirmed, err := collectInteractiveSecrets(ctx, input, app, resolvedScope, class, limits)
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "No secrets saved.")
		return 0
	}
	if err := ctx.Err(); err != nil {
		return startInputExit(err)
	}
	if code := writeSecretsPairs(ctx, client, app, pairs, selectedScope, selectedClass, false); code != 0 {
		return code
	}
	// Plaintext is no longer needed while waiting for the restart decision.
	for i := range pairs {
		pairs[i].Value = ""
	}
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	restart, err := prompt.confirm(ctx, "Secrets saved. Request a fresh restart of "+app+" now?")
	if err != nil {
		return startInputExit(err)
	}
	if restart {
		if err := ctx.Err(); err != nil {
			return startInputExit(err)
		}
		out, err := client.RestartAppFresh(ctx, app)
		if err != nil {
			return printErr("Secrets saved, but restart failed", err)
		}
		PrintOK(osStdout, "Restart requested (wake_id=%s)", out.WakeID)
	}
	return 0
}

func collectInteractiveSecrets(ctx context.Context, input *os.File, app, scope, class string, limits api.Limits) ([]secretsPair, string, string, bool, error) {
	state, err := term.MakeRaw(int(input.Fd()))
	if err != nil {
		return nil, "", "", false, err
	}
	defer term.Restore(int(input.Fd()), state)
	stream := secretTerminalIO{input: input, output: osStderr}
	terminal := &secretEntryTerminal{Terminal: term.NewTerminal(stream, ""), input: stream, maxBytes: limits.SecretValueMaxBytes}
	_, _ = fmt.Fprintln(terminal, "Values are hidden. Ctrl-C or Ctrl-D cancels before saving. Enter an empty name when finished.")
	_, _ = fmt.Fprintf(terminal, "Destination app: %s; scope: %s\n", app, scopeOrDefault(scope))
	_, _ = fmt.Fprintln(terminal, "Scope selects the environment's sealed secret namespace. Enter keeps the displayed scope.")
	var value string
	for {
		value, err = secretTerminalRead(ctx, terminal, "Scope ["+scopeOrDefault(scope)+"]", false)
		if err != nil {
			return nil, "", "", false, err
		}
		if value = strings.TrimSpace(value); value != "" {
			scope = value
		}
		if problem := api.ValidateScope(scopeOrDefault(scope)); problem != nil {
			_, _ = fmt.Fprintln(terminal, problem.Detail)
			continue
		}
		break
	}
	_, _ = fmt.Fprintln(terminal, "Retention: preserve keeps existing classes (new secrets use persistent); ephemeral disables VM snapshots for this scope and forces cold boots.")
	if class == "" {
		class = "preserve"
	}
	for {
		value, err = secretTerminalRead(ctx, terminal, "Retention: preserve, persistent, or ephemeral ["+class+"]", false)
		if err != nil {
			return nil, "", "", false, err
		}
		if value = strings.TrimSpace(value); value == "" {
			value = class
		}
		if value == "preserve" || value == api.SecretClassPersistent || value == api.SecretClassEphemeral {
			class = value
			break
		}
		_, _ = fmt.Fprintln(terminal, "Choose preserve, persistent, or ephemeral.")
	}
	pairs := []secretsPair{}
	seen := map[string]bool{}
	for {
		key, err := secretTerminalRead(ctx, terminal, "Secret name (empty finishes)", false)
		if err != nil {
			return nil, "", "", false, err
		}
		key = strings.TrimSpace(key)
		if key == "" {
			break
		}
		if problem := api.ValidateSecretKey(key); problem != nil {
			_, _ = fmt.Fprintln(terminal, problem.Detail)
			continue
		}
		if seen[key] {
			_, _ = fmt.Fprintln(terminal, "That name is already in this batch.")
			continue
		}
		if len(pairs) >= limits.SecretCountMax {
			_, _ = fmt.Fprintln(terminal, "This batch reached the plan's secret count limit. Enter an empty name to review it.")
			continue
		}
		value, err := secretTerminalRead(ctx, terminal, "Value for "+key+" (hidden)", true)
		if err != nil {
			return nil, "", "", false, err
		}
		if len(value) > limits.SecretValueMaxBytes {
			_, _ = fmt.Fprintf(terminal, "Value exceeds the plan's %d-byte limit. Enter the name and value again.\n", limits.SecretValueMaxBytes)
			continue
		}
		pairs = append(pairs, secretsPair{Key: key, Value: value})
		seen[key] = true
	}
	if len(pairs) == 0 {
		return nil, scope, class, false, nil
	}
	_, _ = fmt.Fprintf(terminal, "\nReview: app=%s; scope=%s; retention=%s\n", app, scopeOrDefault(scope), class)
	for _, pair := range pairs {
		_, _ = fmt.Fprintf(terminal, "  %s (value hidden)\n", pair.Key)
	}
	_, _ = fmt.Fprintln(terminal, "Existing names will be updated. Keys are saved individually; an error can leave earlier keys saved.")
	answer, err := secretTerminalRead(ctx, terminal, "Save these secrets? (y/N)", false)
	if class == "preserve" {
		class = ""
	}
	return pairs, scope, class, strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes"), err
}
