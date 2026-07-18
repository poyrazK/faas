package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// authedClient builds a client using the stored token, or errors (exit 2) if the
// user isn't logged in.
func authedClient() (*Client, error) {
	tok := loadToken()
	if tok == "" {
		return nil, errAuth(errors.New("not logged in — run 'faas login'"))
	}
	return NewClient(apiBase(), tok), nil
}

// cmdLogin implements `faas login [--token T]` (UX §2.2). The browser-paste flow
// is the default UX; --token is the CI path (gap G5).
func cmdLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	token := fs.String("token", "", "API token (CI/non-interactive)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *token == "" {
		fmt.Fprintln(os.Stderr, "✗ Interactive browser login isn't wired yet.")
		fmt.Fprintln(os.Stderr, "  Use 'faas login --token <token>' (get one from the dashboard).")
		return 1
	}
	client := NewClient(apiBase(), *token)
	acct, err := client.Whoami(context.Background())
	if err != nil {
		return printErr("Login failed", err)
	}
	if err := saveToken(*token); err != nil {
		return printErr("Could not save token", err)
	}
	fmt.Printf("✓ Logged in as %s (%s plan)\n", acct.Email, acct.Plan)
	return 0
}

func cmdLogout() int {
	if p, err := tokenPath(); err == nil {
		_ = os.Remove(p)
	}
	fmt.Println("✓ Logged out")
	return 0
}

func cmdWhoami() int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	acct, err := client.Whoami(context.Background())
	if err != nil {
		return printErr("Request failed", err)
	}
	fmt.Printf("%s (%s plan, %s)\n", acct.Email, acct.Plan, acct.Status)
	return 0
}

func cmdApps() int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	apps, err := client.ListApps(context.Background())
	if err != nil {
		return printErr("Request failed", err)
	}
	if len(apps) == 0 {
		fmt.Println("No apps yet. Deploy one: faas deploy --image <ref>")
		return 0
	}
	for _, a := range apps {
		fmt.Printf("%-24s %-10s %s\n", a.Slug, a.Status, a.URL)
	}
	return 0
}

// deriveName uses the current directory as the default app slug (UX §2.3).
func deriveName() string {
	wd, err := os.Getwd()
	if err != nil {
		return appSlugFallback
	}
	return sanitizeSlug(filepath.Base(wd))
}

// sanitizeSlug lowercases and strips a directory name into a valid slug shape.
func sanitizeSlug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ' || r == '.':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) < 3 {
		out = "app-" + out
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return strings.Trim(out, "-")
}

// printErr renders an error in the CLI's shape and returns the exit code (UX §3).
func printErr(title string, err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		fmt.Fprintf(os.Stderr, "✗ %s\n", ae.Error())
		return exitCodeForStatus(ae.Problem.Status)
	}
	var ec *exitErr
	if errors.As(err, &ec) {
		fmt.Fprintf(os.Stderr, "✗ %s\n  %s\n", title, ec.msg)
		return ec.code
	}
	fmt.Fprintf(os.Stderr, "✗ %s\n  %s\n", title, err.Error())
	return 1
}

func exitCodeForStatus(status int) int {
	switch {
	case status == 401 || status == 402:
		return 2
	case status >= 500:
		return 3
	default:
		return 1
	}
}

// exitErr carries an explicit exit code for non-API errors.
type exitErr struct {
	msg  string
	code int
}

func (e *exitErr) Error() string { return e.msg }

func errAuth(err error) error { return &exitErr{msg: err.Error(), code: 2} }
