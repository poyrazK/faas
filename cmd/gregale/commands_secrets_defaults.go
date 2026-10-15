package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type localSecretDefaults struct {
	Version      int      `json:"version"`
	Environments []string `json:"environments"`
}

func secretDefaultsPath() (string, error) {
	path, err := jobDefaultsPath()
	if err != nil {
		return "", fmt.Errorf("locate project defaults: %w", err)
	}
	return filepath.Join(filepath.Dir(path), "secrets.json"), nil
}

func readSecretDefaults() (localSecretDefaults, string, error) {
	var defaults localSecretDefaults
	path, err := secretDefaultsPath()
	if err != nil {
		return defaults, path, err
	}
	parent, err := inspectArtifactOutputDirectory(filepath.Dir(path))
	if err != nil {
		return defaults, path, fmt.Errorf("inspect defaults directory: %w", err)
	}
	defer parent.Close()
	if parent.root == nil {
		return defaults, path, os.ErrNotExist
	}
	file, err := openCustomerFile(path)
	if err != nil {
		return defaults, path, fmt.Errorf("open secret defaults: %w", err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil {
		return defaults, path, fmt.Errorf("read secret defaults: %w", err)
	}
	if len(data) > 8192 {
		return defaults, path, errors.New("secret defaults exceed 8 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&defaults); err != nil {
		return defaults, path, errors.New("secret defaults must be a versioned JSON object")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return defaults, path, errors.New("secret defaults must contain one JSON object")
	}
	if defaults.Version != 1 || len(defaults.Environments) == 0 || len(defaults.Environments) > 10 {
		return defaults, path, errors.New("secret defaults require version 1 and 1–10 scope names")
	}
	for _, scope := range defaults.Environments {
		if scope == "__all__" || api.ValidateScope(scope) != nil {
			return defaults, path, errors.New("secret defaults contain an invalid scope name")
		}
	}
	scopes, err := parseSecretComparisonScopes(strings.Join(defaults.Environments, ","))
	if err != nil {
		return defaults, path, err
	}
	defaults.Environments = scopes
	return defaults, path, nil
}

func cmdSecretDefaults(args []string) int {
	if len(args) == 0 {
		args = []string{"show"}
	}
	path, err := secretDefaultsPath()
	if err != nil {
		return printErr("Could not locate secret defaults", err)
	}
	var defaults localSecretDefaults
	switch args[0] {
	case "set":
		fs := newFlagSet("secrets defaults set", flag.ContinueOnError)
		environments := fs.String("environments", "", "comma-separated scope names for saved comparisons")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if rejectUnexpectedFlagArgs(fs) {
			return 1
		}
		scopes, err := parseSecretComparisonScopes(*environments)
		if err != nil {
			return printErr("Invalid saved environments", err)
		}
		defaults = localSecretDefaults{Version: 1, Environments: scopes}
		if err := writeSecretDefaults(path, defaults); err != nil {
			return printErr("Could not save secret defaults", err)
		}
	case "show":
		if len(args) != 1 {
			return printErr("Invalid defaults arguments", errors.New("show accepts no arguments"))
		}
		defaults, path, err = readSecretDefaults()
		if err != nil {
			return printErr("Could not read secret defaults", err)
		}
	case "clear":
		if len(args) != 1 {
			return printErr("Invalid defaults arguments", errors.New("clear accepts no arguments"))
		}
		parent, err := inspectArtifactOutputDirectory(filepath.Dir(path))
		if err != nil {
			return printErr("Could not inspect defaults directory", err)
		}
		defer parent.Close()
		if parent.root != nil {
			info, err := parent.root.Lstat(filepath.Base(path))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return printErr("Could not inspect defaults", err)
			}
			if err == nil {
				if !info.Mode().IsRegular() {
					return printErr("Invalid defaults file", errors.New("clear requires a regular defaults file, not a symlink or directory"))
				}
				if err := parent.root.Remove(filepath.Base(path)); err != nil {
					return printErr("Could not clear secret defaults", err)
				}
			}
		}
		if jsonOutput {
			return jsonOut(writeJSON(struct {
				Cleared bool   `json:"cleared"`
				Path    string `json:"path"`
			}{true, path}))
		}
		_, _ = fmt.Fprintf(osStdout, "Secret comparison defaults cleared: %s\n", oneLine(path))
		return 0
	default:
		return printErr("Invalid secret defaults command", errors.New("use set, show, or clear"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			Defaults localSecretDefaults `json:"defaults"`
			Path     string              `json:"path"`
		}{defaults, path}))
	}
	_, _ = fmt.Fprintf(osStdout, "Saved comparison scopes: %s\nPath: %s\n", strings.Join(defaults.Environments, ", "), oneLine(path))
	return 0
}

func writeSecretDefaults(path string, defaults localSecretDefaults) error {
	return writeLocalSecretConfig(path, defaults)
}

func writeLocalSecretConfig(path string, value any) error {
	directory := filepath.Dir(path)
	if err := os.Mkdir(directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create defaults directory: %w", err)
	}
	parent, err := inspectArtifactOutputDirectory(directory)
	if err != nil {
		return fmt.Errorf("inspect defaults directory: %w", err)
	}
	defer parent.Close()
	if parent.root == nil {
		return errors.New("defaults directory disappeared")
	}
	name := filepath.Base(path)
	if info, err := parent.root.Lstat(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect defaults: %w", err)
	} else if err == nil && !info.Mode().IsRegular() {
		return errors.New("defaults destination must be a regular file, not a symlink or directory")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode secret defaults: %w", err)
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("prepare defaults output: %w", err)
	}
	temporary := ".secret-defaults-" + hex.EncodeToString(nonce[:]) + ".tmp"
	file, err := parent.root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create defaults output: %w", err)
	}
	defer func() { _ = file.Close(); _ = parent.root.Remove(temporary) }()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write secret defaults: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync secret defaults: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close secret defaults: %w", err)
	}
	if err := parent.root.Rename(temporary, name); err != nil {
		return fmt.Errorf("save secret defaults: %w", err)
	}
	return nil
}
