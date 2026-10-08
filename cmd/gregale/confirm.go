// confirm.go — issue #312 typed-confirmation helper.
//
// Replaces the historical `y/N` keystroke prompt on destructive
// commands with the typed-string idiom (`gh repo delete`,
// `vercel rm`, `aws s3 rm --recursive`). The user must retype an
// exact string — the slug for app delete, the literal
// `delete my account` for account delete. A copy-paste typo
// followed by `y` no longer deletes the account; the user has to
// actually type the destructive verb.
//
// The `-q` / `--quiet` flag bypasses this helper entirely — CI and
// scripted use are legitimate paths and the user has accepted that
// trade-off by passing the flag.

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// requireTyped prints `Type %q to confirm: ` on stderr, reads one
// line from the osStdin seam (production: os.Stdin, tests:
// pipeStdin in cli_login_test.go:61), and returns true only on an
// exact-byte match against expected.
//
// Policy decisions baked into the helper, mirrored in the
// confirm_test.go table:
//
//   - TrimRight of CR/LF only (NOT TrimSpace). Leading or trailing
//     whitespace in the typed line is treated as a mismatch on
//     purpose: `  delete my account  ` would otherwise pass and
//     defeat the safety story. Newline / CR are the only legitimate
//     terminal-induced noise.
//   - Case-sensitive. `Delete My Account` is a mismatch.
//   - EOF / read error → false. We do not distinguish "user hit
//     Ctrl-D" from "stdin closed unexpectedly" because both collapse
//     to "abort the destructive operation" — the safe default.
//   - No os.Exit inside the helper. Callers decide the exit code so
//     tests can assert `code == 1` after a wrong-typed input.
//
// Returns true → caller proceeds with the destructive action.
// Returns false → caller MUST abort (typically `return 1`).
func requireTyped(expected string) bool {
	if nonInteractive {
		printErr("Confirmation required", errors.New("use an explicit confirmation flag in non-interactive mode"))
		return false
	}
	// production-us hunt #4: a script without --quiet sat on this prompt
	// until its timeout, with nothing in its log saying why. Piped input
	// still works; the hint names the bypass.
	if f, ok := osStdin.(*os.File); ok && !term.IsTerminal(int(f.Fd())) {
		_, _ = fmt.Fprintf(osStderr, "stdin is not a terminal: pipe %q in, or pass --quiet to skip this confirmation.\n", expected)
	}
	_, _ = fmt.Fprintf(osStderr, "Type %q to confirm: ", expected)
	line, err := readConfirmationLine(osStdin)
	if err != nil && line == "" {
		_, _ = fmt.Fprintln(osStderr, "Operation cancelled")
		return false
	}
	if line != expected {
		_, _ = fmt.Fprintln(osStderr, "Operation cancelled")
		return false
	}
	return true
}

// readConfirmationLine reads one line from r, trimming a single
// trailing CR/LF pair (handles both Unix \n and Windows \r\n line
// endings). Returns the trimmed line and the scan error so the
// caller can distinguish "EOF" from "garbled input".
func readConfirmationLine(r io.Reader) (string, error) {
	if nonInteractive { return "", errors.New("confirmation input is disabled by --non-interactive; supply the explicit confirmation flag") }
	sc := bufio.NewScanner(r)
	// Default ScanLines strips the trailing \n but keeps a trailing
	// \r on \r\n inputs. Buffer cap is 64 KiB — a confirmation
	// prompt is one line of input, well under that.
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return strings.TrimRight(sc.Text(), "\r\n"), nil
}
