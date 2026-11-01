// adr: 20261018170742 — one app serves the Data API and runs migrations as its release task.
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	dataAPIMigrationEnv            = "MIGRATION_DATABASE_URL"
	dataAPIDefaultReleaseCommand   = "node migrations/release.mjs"
	dataAPISingleAppArchiveRoot    = "data-api"
	dataAPISingleAppDockerfileHook = "EXPOSE 8080"
)

func validDataAPIReleaseCommand(command string) bool {
	return strings.TrimSpace(command) != "" && len(command) <= api.DataAPIReleaseCommandMaxBytes && !strings.ContainsAny(command, "\r\n\x00")
}

type dataAPIArchiveFile struct {
	name    string
	mode    int64
	content []byte
}

// Customer migration files: regular files only, no hidden entries, dependencies
// or links, within the structural bounds. Paths stay relative to the directory.
func readDataAPIMigrations(dir string) ([]dataAPIArchiveFile, error) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("migrations directory %q is not readable", dir)
	}
	var files []dataAPIArchiveFile
	var total int64
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file; links and devices are not uploaded", filepath.ToSlash(rel))
		}
		content, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		total += int64(len(content))
		if len(files)+1 > api.DataAPISingleAppMigrationsMaxFiles || total > api.DataAPISingleAppMigrationsMaxBytes {
			return fmt.Errorf("migrations exceed %d files or %d bytes", api.DataAPISingleAppMigrationsMaxFiles, api.DataAPISingleAppMigrationsMaxBytes)
		}
		files = append(files, dataAPIArchiveFile{name: "migrations/" + filepath.ToSlash(rel), mode: 0o644, content: content})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("migrations directory is empty")
	}
	return files, nil
}

// buildDataAPISingleAppArchive writes the runtime plus the project's
// migrations as one source archive. The Procfile release line and the
// Dockerfile's migration copy are generated; customer files cannot replace
// runtime files because they live only under migrations/.
func buildDataAPISingleAppArchive(runtime dataAPIRuntimeBundle, migrationsDir, releaseCommand, dest string) error {
	migrations, err := checkedDataAPIMigrations(migrationsDir, releaseCommand)
	if err != nil {
		return err
	}
	dockerfile, ok := runtime.Files["Dockerfile"]
	if !ok {
		return errors.New("data-api runtime has no Dockerfile")
	}
	if strings.Count(string(dockerfile), dataAPISingleAppDockerfileHook) != 1 {
		return errors.New("data-api runtime Dockerfile changed; cannot add migrations")
	}
	files := dataAPIRuntimeArchiveFiles(runtime, "Dockerfile", "Procfile")
	// Release tasks run in the runtime image, which already has the pinned pg driver.
	files = append(files,
		dataAPIArchiveFile{name: "Dockerfile", mode: 0o644, content: []byte(strings.Replace(string(dockerfile), dataAPISingleAppDockerfileHook, "COPY migrations ./migrations\n"+dataAPISingleAppDockerfileHook, 1))},
		dataAPIArchiveFile{name: "Procfile", mode: 0o644, content: []byte("web: node server.mjs\nrelease: " + releaseCommand + "\n")},
	)
	files = append(files, migrations...)
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return writeDataAPIArchive(dest, files)
}

// checkedDataAPIMigrations reads the migration directory and verifies the
// release entrypoint, so create can fail before it reserves any resources.
func checkedDataAPIMigrations(dir, releaseCommand string) ([]dataAPIArchiveFile, error) {
	if !validDataAPIReleaseCommand(releaseCommand) {
		return nil, errors.New("release command must be one non-empty line")
	}
	migrations, err := readDataAPIMigrations(dir)
	if err != nil {
		return nil, err
	}
	if releaseCommand == dataAPIDefaultReleaseCommand && !hasDataAPIArchiveFile(migrations, "migrations/release.mjs") {
		return nil, errors.New("migrations/release.mjs is required for the default release command; pass --release-command to use another entrypoint")
	}
	return migrations, nil
}

func hasDataAPIArchiveFile(files []dataAPIArchiveFile, name string) bool {
	for _, f := range files {
		if f.name == name {
			return true
		}
	}
	return false
}

func writeDataAPIArchive(dest string, files []dataAPIArchiveFile) (err error) {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
	}()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		if err = tw.WriteHeader(&tar.Header{Name: dataAPISingleAppArchiveRoot + "/" + f.name, Mode: f.mode, Size: int64(len(f.content)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err = tw.Write(f.content); err != nil {
			return err
		}
	}
	if err = tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// Attach the release-only migration credential to the Data API app itself.
func attachDataAPIMigrationBinding(ctx context.Context, client *api.Client, app api.AppResponse, database, scope string) error {
	binding, err := client.CreateManagedPostgresBinding(api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("data-api-migration", app.ID, database, scope)), database, api.CreateManagedPostgresBindingRequest{AppID: app.ID, Scope: scope, EnvironmentKey: dataAPIMigrationEnv, Access: "migration"})
	if err != nil {
		return err
	}
	_, err = waitForManagedPostgresBinding(ctx, client, binding, addResourceDefaultWait)
	return err
}

// deploySingleAppDataAPI uploads the runtime and migrations; the deployment's
// release task applies migrations before new instances load the schema.
func deploySingleAppDataAPI(ctx context.Context, client *api.Client, slug, scope, migrationsDir, releaseCommand, timeout string) int {
	runtime, err := resolveDataAPIRuntime(ctx, client, slug)
	if err != nil {
		return printErr("Could not get the Data API runtime", err)
	}
	reportDataAPIRuntime(runtime)
	return deployDataAPIArchive(ctx, slug, scope, timeout, func(dest string) error {
		return buildDataAPISingleAppArchive(runtime, migrationsDir, releaseCommand, dest)
	})
}

// deployDataAPIArchive writes the source archive and deploys it to the app.
func deployDataAPIArchive(ctx context.Context, slug, scope, timeout string, write func(dest string) error) int {
	dir, err := os.MkdirTemp("", "gregale-data-api-*")
	if err != nil {
		return printErr("Could not prepare the Data API archive", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	archive := filepath.Join(dir, "data-api.tar.gz")
	if err = write(archive); err != nil {
		return printErr("Could not build the Data API archive", err)
	}
	deployArgs := []string{"--name", slug, "--tarball", archive, "--dockerfile", "--healthcheck-path", "/healthz", "--no-doctor"}
	if timeout != "" {
		deployArgs = append(deployArgs, "--timeout", timeout)
	}
	if scope != api.DefaultEnvScope {
		deployArgs = append(deployArgs, "--environment", scope)
	}
	return cmdDeployTarballToExisting(ctx, deployArgs, true)
}

func cmdDataAPIDeploy(args []string) int {
	fs := newFlagSet("data-api deploy", flag.ContinueOnError)
	migrations := fs.String("migrations", "", "migration directory to run as this app's release task")
	releaseCommand := fs.String("release-command", dataAPIDefaultReleaseCommand, "release command run with "+dataAPIMigrationEnv)
	scope := fs.String("scope", api.DefaultEnvScope, "environment scope")
	timeout := fs.String("timeout", "15m", "deployment and release wait (seconds or duration)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	releaseCommandSet := false
	fs.Visit(func(f *flag.Flag) { releaseCommandSet = releaseCommandSet || f.Name == "release-command" })
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || (releaseCommandSet && *migrations == "") || api.ValidateScope(*scope) != nil || !validDataAPIReleaseCommand(*releaseCommand) {
		PrintUsage(os.Stderr, "usage: gregale data-api deploy NAME [--migrations DIR [--release-command COMMAND]] [--env SCOPE] [--timeout DURATION]", "data-api")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	// Apps created before the runtime had lifecycle hooks get them with the
	// runtime that serves them.
	ctx := context.Background()
	update := api.UpdateAppRequest{}
	setDataAPILifecycle(&update)
	if _, err = client.UpdateApp(ctx, fs.Arg(0), update); err != nil {
		return printErr("Could not set the Data API lifecycle hooks", err)
	}
	if *migrations == "" {
		// The runtime alone: an upgrade, or an API whose schema is managed elsewhere.
		return deployDataAPIRuntime(ctx, client, fs.Arg(0), *scope, *timeout)
	}
	return deploySingleAppDataAPI(ctx, client, fs.Arg(0), *scope, *migrations, *releaseCommand, *timeout)
}
