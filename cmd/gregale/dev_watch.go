package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// devWatchDefaultCommand runs the package's dev script.
const devWatchDefaultCommand = "npm run dev"

// applyDevWatchManifestDefault lets dev.watch in gregale.yaml supply
// --watch and --watch-command when neither flag was given.
func applyDevWatchManifestDefault(manifest *gregalemanifest.Manifest, explicit map[string]bool, watch *bool, command *string) {
	if manifest == nil || manifest.Dev == nil || manifest.Dev.Watch == nil || explicit["watch"] || explicit["watch-command"] {
		return
	}
	*watch = manifest.Dev.Watch.Enabled
	*command = manifest.Dev.Watch.Command
}

// resolveDevWatch turns --watch / --watch-command into the developer session
// setting (ADR-1170); nil means watch mode is off. Without an explicit command
// it runs the package's dev script or the detected framework's development
// server (devWatchDefault). notes are hints for the developer.
func resolveDevWatch(sourceDir string, config devSourceConfig, enabled bool, command string) (*api.DevWatch, []string, error) {
	command = strings.TrimSpace(command)
	if !enabled && command == "" {
		return nil, nil, nil
	}
	if config.shape == shapeFunction {
		return nil, nil, errors.New("watch mode is for apps; functions run through the platform runner")
	}
	var notes []string
	if command == "" {
		pkg, err := readDevWatchPackage(sourceDir)
		if err != nil {
			return nil, nil, err
		}
		var ok bool
		if command, notes, ok = devWatchDefault(sourceDir, pkg); !ok {
			return nil, nil, errors.New(`add a "dev" script to package.json or pass --watch-command, for example --watch-command "next dev"; without one, watch mode recognizes Next.js, Vite, Nuxt, Astro, SvelteKit, Remix and React Router`)
		}
	}
	if len(command) > api.DevWatchCommandMaxBytes || strings.ContainsAny(command, "\n\r\x00") {
		return nil, nil, fmt.Errorf("the watch command must be one line of at most %d bytes", api.DevWatchCommandMaxBytes)
	}
	return &api.DevWatch{Command: command}, notes, nil
}

// readDevWatchPackage reads sourceDir/package.json. A missing package.json
// means this is not a Node.js app.
func readDevWatchPackage(sourceDir string) (devWatchPackage, error) {
	var pkg devWatchPackage
	data, err := os.ReadFile(filepath.Join(sourceDir, "package.json"))
	if errors.Is(err, os.ErrNotExist) {
		return pkg, errors.New("watch mode supports Node.js apps; no package.json in the source directory")
	}
	if err != nil {
		return pkg, fmt.Errorf("read package.json: %w", err)
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return pkg, fmt.Errorf("parse package.json: %w", err)
	}
	return pkg, nil
}

// devWatchSuggestion is the command gregale dev --watch would run for
// sourceDir, or "" when watch mode cannot pick one there.
func devWatchSuggestion(sourceDir string) string {
	pkg, err := readDevWatchPackage(sourceDir)
	if err != nil {
		return ""
	}
	command, _, _ := devWatchDefault(sourceDir, pkg)
	return command
}
