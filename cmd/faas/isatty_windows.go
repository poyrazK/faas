//go:build windows

// Windows stub for the §3.2 stdout-TTY gate (output.go). The CLI does
// not officially target Windows today — every load-bearing customer
// runs the production binary on a Hetzner EX44 (Linux). The stub keeps
// `go build ./...` from breaking on a contributor's Windows box, and
// it deliberately reports "not a TTY" so a customer who somehow runs
// faas.exe in cmd.exe still gets the same plain-text behaviour that a
// pipe would produce: no surprise glyphs in scripts that capture stdout.
//
// If a real Windows port is ever needed, replace this body with
// `kernel32.GetConsoleMode` against `os.Stdout.Fd()` (or call into
// `golang.org/x/term.IsTerminal`, which already does this dance).
package main

// stdoutIsTTY always reports false on Windows. See file header.
// Defined here (with a `//go:build windows` tag) so it shadows the
// unix implementation in isatty_unix.go on a Windows build.
func stdoutIsTTY() bool {
	if testOnlyTTY != nil {
		return *testOnlyTTY
	}
	return false
}
