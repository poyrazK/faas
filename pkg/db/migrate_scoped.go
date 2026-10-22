package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	"github.com/onebox-faas/faas/migrations"
	"github.com/pressly/goose/v3"
)

func applyScopedMigrations(ctx context.Context, sqlDB *sql.DB, allowOutOfOrder bool) error {
	sources, err := scopedMigrationSources(ctx, sqlDB)
	if err != nil {
		return err
	}
	// A per-run provider prevents another schema's migration sources from
	// replacing this run's filesystem through Goose's global registry.
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, sources,
		goose.WithAllowOutofOrder(allowOutOfOrder), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return fmt.Errorf("migration provider: %w", err)
	}
	_, err = provider.Up(ctx)
	return err
}

// Issued SQL remains immutable. These declarations were copied from a public
// schema dump; bind them to the migration target when running in another schema.
// Public migrations consume the original embedded bytes without adaptation.
func scopedMigrationSources(ctx context.Context, sqlDB *sql.DB) (fs.FS, error) {
	var schema sql.NullString
	if err := sqlDB.QueryRowContext(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		return nil, fmt.Errorf("read migration schema: %w", err)
	}
	if !schema.Valid {
		return nil, fmt.Errorf("migration search_path has no existing schema")
	}
	return migrations.SourcesForSchema(schema.String), nil
}
