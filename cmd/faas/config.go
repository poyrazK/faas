package main

import (
	"os"
	"path/filepath"
	"strings"
)

// CLI configuration: the API base URL and the auth token. The token is read from
// $FAAS_TOKEN (CI) or a config file. NOTE: the OS keychain (macOS Keychain /
// libsecret / wincred, UX §2.2) is the real store; this file-based fallback is a
// clearly-scoped M5 stand-in.

const defaultAPIBase = "https://api.DOMAIN"

// apiBase returns the API base URL, overridable via $FAAS_API for local/dev.
func apiBase() string {
	if v := os.Getenv("FAAS_API"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultAPIBase
}

// tokenPath is where the CLI persists the auth token.
func tokenPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "faas", "token"), nil
}

// loadToken returns the token from $FAAS_TOKEN or the config file.
func loadToken() string {
	if v := os.Getenv("FAAS_TOKEN"); v != "" {
		return strings.TrimSpace(v)
	}
	p, err := tokenPath()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// saveToken persists the token to the config file with 0600 perms.
func saveToken(token string) error {
	p, err := tokenPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strings.TrimSpace(token)+"\n"), 0o600)
}
