package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// devWatchPackage is the part of package.json the Next.js check reads.
type devWatchPackage struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func (p devWatchPackage) has(dep string) bool {
	_, prod := p.Dependencies[dep]
	_, dev := p.DevDependencies[dep]
	return prod || dev
}

// nextDevOriginsExpr is what allowedDevOrigins is set to: the developer
// environment's hostname in a watch-mode image, nothing elsewhere.
const nextDevOriginsExpr = "process.env." + api.DevWatchHostEnv + " ? [process.env." + api.DevWatchHostEnv + "] : []"

var nextConfigNames = []string{"next.config.js", "next.config.mjs", "next.config.cjs", "next.config.ts", "next.config.mts"}

// nextNeedsDevOrigins reports whether a Next.js app has no allowedDevOrigins
// in its config. Next.js reads it only from the config file and otherwise
// drops the hot-reload websocket from the environment's hostname.
func nextNeedsDevOrigins(sourceDir string, pkg devWatchPackage) bool {
	if !pkg.has("next") {
		return false
	}
	for _, name := range nextConfigNames {
		data, err := os.ReadFile(filepath.Join(sourceDir, name))
		if err == nil && strings.Contains(string(data), "allowedDevOrigins") {
			return false
		}
	}
	return true
}

// nextConfigObjectOpen finds the opening brace of the exported config
// object in the shapes create-next-app and the Next.js docs use.
var nextConfigObjectOpen = regexp.MustCompile(`(?m)^(?:(?:const|let|var)\s+nextConfig\b[^=\n]*=|module\.exports\s*=|export\s+default)\s*\{`)

var errNextConfigShape = errors.New("next.config does not export a plain object Gregale can edit")

// addNextDevOrigins sets allowedDevOrigins in sourceDir's next.config,
// creating next.config.mjs when there is none, and returns the file it
// wrote. A config it cannot edit safely is left alone (errNextConfigShape).
func addNextDevOrigins(sourceDir string) (string, error) {
	for _, name := range nextConfigNames {
		path := filepath.Join(sourceDir, name)
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		opens := nextConfigObjectOpen.FindAllIndex(data, -1)
		if len(opens) != 1 {
			return "", fmt.Errorf("%s: %w", name, errNextConfigShape)
		}
		at := opens[0][1]
		edited := string(data[:at]) + "\n  allowedDevOrigins: " + nextDevOriginsExpr + "," + string(data[at:])
		return name, os.WriteFile(path, []byte(edited), info.Mode().Perm())
	}
	const created = "next.config.mjs"
	body := "// Lets Next.js serve hot reload to the Gregale developer environment\n" +
		"// (gregale dev --watch); GREGALE_DEV_HOST is unset everywhere else.\n" +
		"const nextConfig = {\n  allowedDevOrigins: " + nextDevOriginsExpr + ",\n};\n\nexport default nextConfig;\n"
	return created, os.WriteFile(filepath.Join(sourceDir, created), []byte(body), 0o644)
}
