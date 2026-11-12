package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

type devLocalAppFingerprint struct {
	source, restart [sha256.Size]byte
}

// Launch inputs must restart even a framework-owned server. Watch them outside
// source-ignore rules, just like the selected env file (which may be external).
func fingerprintDevLocalApp(root string, config devLocalEnvConfig) (devLocalAppFingerprint, error) {
	source, err := devSourceFingerprint(root)
	if err != nil {
		return devLocalAppFingerprint{}, err
	}
	h := sha256.New()
	env := config.fingerprint()
	_, _ = h.Write(env[:])
	for _, name := range []string{
		"gregale.yaml", "gregale.yml", "gregale.toml", "package.json",
		"package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb",
		".npmrc", ".yarnrc", ".yarnrc.yml", "pnpm-workspace.yaml",
	} {
		path := filepath.Join(root, name)
		info, statErr := os.Stat(path)
		var data []byte
		readable := false
		if statErr == nil && info.Mode().IsRegular() {
			var readErr error
			data, readErr = os.ReadFile(path)
			readable = readErr == nil
		}
		// Missing or unreadable inputs are changes too. Planning/validation
		// reports the error before stopping a currently running server.
		_, _ = fmt.Fprintf(h, "%s\x00%t\x00%x\n", name, readable, sha256.Sum256(data))
	}
	result := devLocalAppFingerprint{source: source}
	copy(result.restart[:], h.Sum(nil))
	return result, nil
}
