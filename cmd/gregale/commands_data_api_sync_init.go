package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

// Generate the existing coordinator's commands, rather than introducing a
// second migration engine. No credentials, SQL or remote work run here.
func initDataAPISync(slug, path, role, runtimeDir string) int {
	if !regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`).MatchString(role) {
		return printErr("Invalid binding role", errors.New("--role must name the Data API serving login"))
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return printErr("Invalid config path", err)
	}
	base := filepath.Dir(absolute)
	if _, err = os.Lstat(absolute); !errors.Is(err, os.ErrNotExist) {
		return printErr("Config already exists", errors.New("choose a new --config path; existing files are preserved"))
	}
	runtime := dataAPISyncPath(base, runtimeDir)
	if runtime == base || !filepath.IsLocal(runtimeDir) {
		return printErr("Invalid runtime directory", errors.New("use a project-relative directory"))
	}
	if err = checkDestEmpty(runtime); err != nil {
		return printErr("Runtime directory is not empty", err)
	}
	step := func(args ...string) dataAPISyncCommand { return dataAPISyncCommand{Command: args, Directory: "."} }
	owner := func(action string) dataAPISyncCommand {
		return step("node", filepath.ToSlash(filepath.Join(runtimeDir, "refresh.mjs")), action, role)
	}
	setup := owner("check")
	config := dataAPISyncConfig{Output: "client/src/database.types.ts", Migrate: step("gregale", "data-api", "deploy", slug, "--migrations", "migrations"), Check: dataAPISyncCommand{Command: []string{"npm", "run", "typecheck"}, Directory: "client"}, Coordination: &dataAPISyncCoordination{Baseline: "schema.before.json", SetupCheck: &setup, Pause: owner("pause"), Resume: owner("resume"), Authorization: dataAPISyncCommand{Command: []string{"npm", "run", "test:authorization"}, Directory: "client"}}}
	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return printErr("Could not encode workflow", err)
	}
	if err = templates.Materialize("data-api", runtime); err != nil {
		return printErr("Could not prepare owner tooling", err)
	}
	file, err := os.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return printErr("Could not create workflow", err)
	}
	_, err = file.Write(append(content, '\n'))
	closeErr := file.Close()
	if err != nil {
		return printErr("Could not write workflow", err)
	}
	if closeErr != nil {
		return printErr("Could not close workflow", closeErr)
	}
	PrintOK(osStdout, "Created coordinated workflow %s", absolute)
	_, _ = fmt.Fprintf(osStdout, "Review the migration and authorization commands, then prepare the owner barrier:\n  npm ci --prefix %s\n  node %s/refresh.mjs install %s\n  gregale env set --app %s DATA_API_COORDINATED_REFRESH=1\n  gregale data-api refresh %s --wait\n  gregale data-api types %s --snapshot --output schema.before.json\nSet MIGRATION_DATABASE_URL, GREGALE_DATA_API_ACCESS_TOKEN and a private GREGALE_DATA_API_REFRESH_TOKEN through your secret environment.\nRun: gregale data-api sync %s --config %s --check\nThen run without --check. If paused after a failure, correct the failure and use --resume-only with the same refresh token.\n", runtimeDir, runtimeDir, role, slug, slug, slug, slug, path)
	return 0
}
