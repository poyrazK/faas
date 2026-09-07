// Package daemonenv loads the daemon environment contract at process start.
// Keeping this small and independent from the command packages makes the
// fail-closed behaviour identical for every daemon while preserving a
// deterministic lookup seam for unit tests.
package daemonenv

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

// Env is the resolved view of the contract for one daemon. Values contains
// only declared contract names; defaults are materialized before returning.
// Missing is retained on an error so callers and tests can inspect the full
// set without parsing an error string.
type Env struct {
	Daemon  string
	Values  map[string]string
	Missing []string
}

// Get returns a resolved value or the empty string when the name is not in
// the daemon's contract. It is suitable for the getenv seams used by daemon
// configuration helpers.
func (e Env) Get(name string) string {
	return e.Values[name]
}

// Lookup returns a resolved value and whether the name was declared and
// present. A declared optional variable with an empty value returns false;
// this preserves the usual os.LookupEnv distinction for callers that need it.
func (e Env) Lookup(name string) (string, bool) {
	v, ok := e.Values[name]
	return v, ok && v != ""
}

// MissingError is returned when one or more Required contract rows are empty
// or unset. It intentionally includes names only; values must never reach a
// log line or process exit message.
type MissingError struct {
	Daemon string
	Names  []string
}

func (e *MissingError) Error() string {
	return fmt.Sprintf("%s: missing required environment variables: %s", e.Daemon, strings.Join(e.Names, ", "))
}

// ExitCode lets wire.Daemon turn a contract failure into the operator-facing
// exit status 2 while retaining status 1 for ordinary runtime failures.
func (*MissingError) ExitCode() int { return 2 }

// Is allows callers to classify a missing-variable failure without depending
// on the concrete error's fields.
var ErrMissing = errors.New("required daemon environment is missing")

func (*MissingError) Unwrap() error { return ErrMissing }

// ValidationError identifies a malformed or unavailable value without
// including the value itself in the error text.
type ValidationError struct {
	Daemon string
	Name   string
	Kind   daemonunitspec.EnvValidation
	Err    error
}

func (e *ValidationError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("%s: %s failed validation for %s", e.Daemon, e.Name, e.Kind)
	}
	return fmt.Sprintf("%s: %s failed %s validation: %v", e.Daemon, e.Name, e.Kind, e.Err)
}

func (e *ValidationError) Unwrap() error { return e.Err }

// Load resolves the contract for daemon from the process environment.
func Load(daemon string) (Env, error) {
	return LoadFrom(daemon, os.LookupEnv)
}

// LoadFrom is Load with an injectable lookup function. The function returns
// the resolved Env alongside errors so tests can assert all missing names.
func LoadFrom(daemon string, lookup func(string) (string, bool)) (Env, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if !knownDaemon(daemon) {
		return Env{Daemon: daemon, Values: map[string]string{}}, fmt.Errorf("daemonenv: unknown daemon %q", daemon)
	}

	env := Env{Daemon: daemon, Values: make(map[string]string)}
	for _, row := range daemonunitspec.EnvContractForDaemon(daemon) {
		value, ok := lookupValue(row.Name, lookup)
		if !ok || strings.TrimSpace(value) == "" {
			value = row.Default
		}
		if row.Required && strings.TrimSpace(value) == "" {
			env.Missing = append(env.Missing, row.Name)
			continue
		}
		if value != "" {
			env.Values[row.Name] = value
		}
		if value != "" && row.Validate != "" {
			if err := validate(row.Validate, value); err != nil {
				return env, &ValidationError{Daemon: daemon, Name: row.Name, Kind: row.Validate, Err: err}
			}
		}
	}

	sort.Strings(env.Missing)
	if len(env.Missing) > 0 {
		return env, &MissingError{Daemon: daemon, Names: append([]string(nil), env.Missing...)}
	}
	return env, nil
}

// LoadWithLookup is a descriptive alias for LoadFrom used by callers that
// prefer the lookup seam's name in their tests.
func LoadWithLookup(daemon string, lookup func(string) (string, bool)) (Env, error) {
	return LoadFrom(daemon, lookup)
}

func knownDaemon(name string) bool {
	for _, entry := range daemonunitspec.Registry {
		if entry.Name == name {
			return true
		}
	}
	return false
}

func lookupValue(name string, lookup func(string) (string, bool)) (string, bool) {
	value, ok := lookup(name)
	if ok || name != "FAAS_DATABASE_URL" {
		return value, ok
	}
	// Production role files deliberately use DATABASE_URL. Keep the
	// FAAS_DATABASE_URL row as the contract's stable legacy name while
	// accepting the deployed alias.
	return lookup("DATABASE_URL")
}

func validate(kind daemonunitspec.EnvValidation, value string) error {
	switch kind {
	case daemonunitspec.EnvValidationPathExists:
		info, err := os.Stat(value)
		if err != nil {
			return errors.New("path is unavailable")
		}
		if info.IsDir() {
			return fmt.Errorf("path is a directory")
		}
	case daemonunitspec.EnvValidationSocket:
		info, err := os.Stat(value)
		if err != nil {
			return errors.New("socket path is unavailable")
		}
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("path is not a Unix socket")
		}
	case daemonunitspec.EnvValidationURL:
		u, err := url.Parse(value)
		if err != nil {
			return errors.New("URL is malformed")
		}
		if u.Scheme == "" {
			return errors.New("URL has no scheme")
		}
	case daemonunitspec.EnvValidationInt:
		if _, err := strconv.Atoi(value); err != nil {
			return errors.New("value is not an integer")
		}
	default:
		return fmt.Errorf("unknown validation kind %q", kind)
	}
	return nil
}
