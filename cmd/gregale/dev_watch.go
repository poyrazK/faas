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
// setting (ADR-970); nil means watch mode is off. Without an explicit command
// it runs the package's dev script.
func resolveDevWatch(sourceDir string, config devSourceConfig, enabled bool, command string) (*api.DevWatch, error) {
	command = strings.TrimSpace(command)
	if !enabled && command == "" {
		return nil, nil
	}
	if config.shape == shapeFunction {
		return nil, errors.New("watch mode is for apps; functions run through the platform runner")
	}
	if command == "" {
		hasDev, err := packageHasDevScript(sourceDir)
		if err != nil {
			return nil, err
		}
		if !hasDev {
			return nil, errors.New(`add a "dev" script to package.json or pass --watch-command, for example --watch-command "next dev"`)
		}
		command = devWatchDefaultCommand
	}
	if len(command) > api.DevWatchCommandMaxBytes || strings.ContainsAny(command, "\n\r\x00") {
		return nil, fmt.Errorf("the watch command must be one line of at most %d bytes", api.DevWatchCommandMaxBytes)
	}
	return &api.DevWatch{Command: command}, nil
}

// packageHasDevScript reports whether sourceDir/package.json defines a dev
// script. A missing package.json means this is not a Node.js app.
func packageHasDevScript(sourceDir string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(sourceDir, "package.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, errors.New("watch mode supports Node.js apps; no package.json in the source directory")
	}
	if err != nil {
		return false, fmt.Errorf("read package.json: %w", err)
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false, fmt.Errorf("parse package.json: %w", err)
	}
	return strings.TrimSpace(pkg.Scripts["dev"]) != "", nil
}
