// Command migration-new creates a timestamp-versioned Goose migration.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/migrations"
)

var migrationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func main() {
	if err := run(os.Args[1:], time.Now); err != nil {
		fmt.Fprintf(os.Stderr, "migration-new: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, now func() time.Time) error {
	flags := flag.NewFlagSet("migration-new", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	name := flags.String("name", "", "lower_snake_case migration name")
	dir := flags.String("dir", "migrations", "migration directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return errors.New("-name is required (example: -name add_job_priority)")
	}
	if !migrationNamePattern.MatchString(*name) {
		return fmt.Errorf("invalid name %q: use lower_snake_case beginning with a letter", *name)
	}

	version := migrations.TimestampMigrationVersion(now())
	if !migrations.IsTimestampMigrationVersion(version) {
		return fmt.Errorf("system UTC clock produced version %d before timestamp cutover %d", version, migrations.TimestampMigrationMinVersion)
	}
	filename := fmt.Sprintf("%017d_%s.sql", version, *name)
	path := filepath.Join(*dir, filename)
	content := fmt.Sprintf(`-- filename: %s

-- +goose Up
-- +goose StatementBegin
-- Write the additive forward migration here.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Write the rollback here.
-- +goose StatementEnd
`, filename)

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; rerun to obtain a new timestamp", path)
		}
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	fmt.Println(path)
	return nil
}
