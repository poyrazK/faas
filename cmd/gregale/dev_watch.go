package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// applyDevWatchManifestDefault lets dev.watch in gregale.yaml supply
// --watch, and its command whenever watch mode is on without
// --watch-command (gregale dev --all --watch passes only --watch).
func applyDevWatchManifestDefault(manifest *gregalemanifest.Manifest, explicit map[string]bool, watch *bool, command *string) {
	if manifest == nil || manifest.Dev == nil || manifest.Dev.Watch == nil || explicit["watch-command"] {
		return
	}
	if !explicit["watch"] {
		*watch = manifest.Dev.Watch.Enabled
	}
	if *watch {
		*command = manifest.Dev.Watch.Command
	}
}

var errDevWatchNoCommand = errors.New(`watch mode could not pick a development command: add a "dev" script to package.json or pass --watch-command, for example --watch-command "uvicorn main:app --reload"; without one it recognizes Next.js, Vite, Nuxt, Astro, SvelteKit, Remix, React Router, Django, FastAPI and Flask apps`)

// resolveDevWatch turns --watch / --watch-command into the developer session
// setting (ADR-1170); nil means watch mode is off. Without an explicit command
// it runs the detected development server (frameworkprofile.DevServerCommand).
func resolveDevWatch(sourceDir string, config devSourceConfig, enabled bool, command string) (*api.DevWatch, error) {
	command = strings.TrimSpace(command)
	if !enabled && command == "" {
		return nil, nil
	}
	if config.shape == shapeFunction {
		return nil, errors.New("watch mode is for apps; functions run through the platform runner")
	}
	if command == "" {
		if command = devWatchSuggestion(sourceDir); command == "" {
			return nil, errDevWatchNoCommand
		}
	}
	if len(command) > api.DevWatchCommandMaxBytes || strings.ContainsAny(command, "\n\r\x00") {
		return nil, fmt.Errorf("the watch command must be one line of at most %d bytes", api.DevWatchCommandMaxBytes)
	}
	return &api.DevWatch{Command: command}, nil
}

// devWatchSuggestion is the command gregale dev --watch would run for
// sourceDir, or "" when watch mode cannot pick one there.
func devWatchSuggestion(sourceDir string) string {
	server, _ := frameworkprofile.DevServerCommand(os.DirFS(sourceDir))
	return server.Command
}

// prepareDevWatchSource checks the settings a development server needs in
// the source itself before the first watch-mode sync, and returns notes to
// show. A Next.js app without allowedDevOrigins drops hot reload from the
// environment's hostname: with a terminal, the developer is asked whether
// Gregale may add it. Django answers 400 to hosts outside ALLOWED_HOSTS, a
// settings change only the developer can make.
func prepareDevWatchSource(sourceDir string, interactive bool, in io.Reader, out io.Writer) []string {
	var notes []string
	var pkg devWatchPackage
	if data, err := os.ReadFile(filepath.Join(sourceDir, "package.json")); err == nil && json.Unmarshal(data, &pkg) == nil && nextNeedsDevOrigins(sourceDir, pkg) {
		notes = append(notes, offerNextDevOrigins(sourceDir, interactive, in, out))
	}
	if server, _ := frameworkprofile.DevServerCommand(os.DirFS(sourceDir)); server.Framework == "Django" && !pythonSourceMentions(sourceDir, api.DevWatchHostEnv) {
		notes = append(notes, `Django answers 400 to hosts outside ALLOWED_HOSTS; add os.environ.get("`+api.DevWatchHostEnv+`", "") to ALLOWED_HOSTS in your settings`)
	}
	return notes
}

// offerNextDevOrigins asks to add allowedDevOrigins and reports the outcome.
func offerNextDevOrigins(sourceDir string, interactive bool, in io.Reader, out io.Writer) string {
	manual := "Next.js drops hot reload from origins outside allowedDevOrigins; add allowedDevOrigins: " + nextDevOriginsExpr + " to next.config"
	if !interactive {
		return manual
	}
	_, _ = fmt.Fprint(out, "Next.js serves hot reload only to origins in allowedDevOrigins. Add this environment's hostname to next.config? [Y/n] ")
	answer, err := readConfirmationLine(in)
	if err != nil || (answer != "" && !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes")) {
		return manual
	}
	name, err := addNextDevOrigins(sourceDir)
	if err != nil {
		return "could not edit next.config (" + err.Error() + "); " + manual
	}
	return "added allowedDevOrigins to " + name + "; it is empty outside the developer environment"
}

// pythonSourceMentions reports whether any Python file under sourceDir
// contains needle; it stops after a bounded walk.
func pythonSourceMentions(sourceDir, needle string) bool {
	found, seen := false, 0
	_ = filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || found || seen > 512 {
			return filepath.SkipAll
		}
		if entry.IsDir() {
			if base := entry.Name(); path != sourceDir && (strings.HasPrefix(base, ".") || base == "node_modules" || base == "__pycache__") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".py") {
			seen++
			data, readErr := os.ReadFile(path)
			found = readErr == nil && strings.Contains(string(data), needle)
		}
		return nil
	})
	return found
}
