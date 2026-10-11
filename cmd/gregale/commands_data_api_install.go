package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

// Install the reviewed SDK bytes through the existing bundle verifier. This
// does not publish a package or change the repository's licensing policy.
func cmdDataAPIInstall(args []string) int {
	fs := newFlagSet("data-api install", flag.ContinueOnError)
	directory := fs.String("directory", ".", "project directory containing package.json")
	client := fs.String("client-directory", ".", "client directory relative to the project")
	bundle := fs.String("bundle", "", "approved local data-api-bundle.json")
	restore := fs.Bool("restore", false, "restore the committed artifact pin")
	from := fs.String("from", "", "local artifact directory for restoration")
	base := fs.String("base-url", "", "immutable HTTPS artifact directory for future restoration")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	if fs.NArg() != 0 || *directory == "" || *client == "" || (*restore == (*bundle != "")) || (*restore && *base != "") || (!*restore && *from != "") {
		PrintUsage(os.Stderr, "usage: gregale data-api install --bundle MANIFEST [--directory DIR] [--client-directory DIR] [--base-url HTTPS_URL] | --restore [--from DIR]", "data-api")
		return 1
	}
	root, err := filepath.Abs(*directory)
	if err != nil {
		return printErr("Invalid project", err)
	}
	if filepath.IsAbs(*client) || !filepath.IsLocal(*client) {
		return printErr("Invalid client directory", errors.New("use a project-relative directory"))
	}
	if info, err := os.Stat(filepath.Join(root, *client, "package.json")); err != nil || !info.Mode().IsRegular() {
		return printErr("Invalid client directory", errors.New("package.json must exist"))
	}
	mode := []string{"restore", "--sdk-only"}
	if !*restore {
		path, err := filepath.Abs(*bundle)
		if err != nil {
			return printErr("Invalid bundle", err)
		}
		mode = []string{"pin", path, "--sdk-only"}
		if *base != "" {
			mode = append(mode, "--base-url", *base)
		}
	} else if *from != "" {
		path, err := filepath.Abs(*from)
		if err != nil {
			return printErr("Invalid artifact directory", err)
		}
		mode = append(mode, "--from", path)
	}
	return installDataAPIBundle(root, *client, mode)
}

func installDataAPIBundle(root, client string, args []string) int {
	runtime, err := os.MkdirTemp("", "gregale-data-api-install-")
	if err != nil {
		return printErr("Could not prepare artifact installer", err)
	}
	defer func() { _ = os.RemoveAll(runtime) }()
	for _, name := range []string{"artifacts.mjs", "artifact-lib.mjs"} {
		content, err := templates.FS.ReadFile("data-api-starter/tools/" + name)
		if err != nil {
			return printErr("Could not prepare artifact installer", err)
		}
		if err := os.WriteFile(filepath.Join(runtime, name), content, 0o600); err != nil {
			return printErr("Could not prepare artifact installer", err)
		}
	}
	options, _ := json.Marshal(struct {
		Root   string   `json:"root"`
		Client string   `json:"client"`
		Args   []string `json:"args"`
	}{root, client, args})
	if err := os.WriteFile(filepath.Join(runtime, "options.json"), options, 0o600); err != nil {
		return printErr("Could not prepare artifact installer", err)
	}
	script := "import {readFile} from 'node:fs/promises'; import {main} from './artifacts.mjs'; const o=JSON.parse(await readFile(new URL('./options.json',import.meta.url),'utf8')); try { await main(o.args,o.root,o.client) } catch { console.error('SDK installation failed; check the approved bundle, artifact checksums, client lockfile and npm configuration.'); process.exitCode=1 }\n"
	if err := os.WriteFile(filepath.Join(runtime, "install.mjs"), []byte(script), 0o600); err != nil {
		return printErr("Could not prepare artifact installer", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := runDataAPISyncCommand(ctx, dataAPISyncCommand{Command: []string{"node", filepath.Join(runtime, "install.mjs")}, Directory: root}); err != nil {
		return printErr("Could not install Data API SDK", err)
	}
	PrintOK(osStdout, "Data API SDK verified and installed; commit the artifact pin and client lockfile")
	return 0
}
