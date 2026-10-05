package db

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"regexp"

	"github.com/jackc/pgx/v5"
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
	if schema.String == "public" {
		return migrations.FS, nil
	}
	return scopedMigrationFS{FS: migrations.FS, schema: pgx.Identifier{schema.String}.Sanitize()}, nil
}

type scopedMigrationFS struct {
	fs.FS
	schema string
}

func (s scopedMigrationFS) Open(name string) (fs.File, error) {
	if !issuedPublicDeclarationMigration(name) {
		return s.FS.Open(name)
	}
	body, err := fs.ReadFile(s.FS, name)
	if err != nil {
		return nil, err
	}
	info, err := fs.Stat(s.FS, name)
	if err != nil {
		return nil, err
	}
	body = scopeIssuedMigrationDeclarations(body, s.schema)
	return &scopedMigrationFile{Reader: bytes.NewReader(body), info: scopedMigrationInfo{FileInfo: info, size: int64(len(body))}}, nil
}

func issuedPublicDeclarationMigration(name string) bool {
	switch name {
	case "20261003160458628_application_standard_runtime_default_base.sql",
		"20261003210400000_application_standard_source_build_rootfs.sql",
		"20261004043519306_application_standard_exception_authority.sql",
		"20261004232943929_application_standard_retained_native_admission.sql",
		"20261004234710285_application_standard_retained_native_protocol.sql":
		return true
	default:
		return false
	}
}

var issuedPublicFunctionDeclaration = regexp.MustCompile(`(?m)^(CREATE(?: OR REPLACE)? FUNCTION )public\.(application_standard_[a-z_]+\()`)
var issuedPublicCompositeDeclaration = regexp.MustCompile(`(?m)^(?:CREATE(?: OR REPLACE)?|DROP) FUNCTION (?:source_build_rootfs_intent|application_standard_runtime_root_producer)\([^\n]*`)

func scopeIssuedMigrationDeclarations(body []byte, schema string) []byte {
	body = issuedPublicFunctionDeclaration.ReplaceAllFunc(body, func(declaration []byte) []byte {
		return bytes.Replace(declaration, []byte("public."), []byte(schema+"."), 1)
	})
	return issuedPublicCompositeDeclaration.ReplaceAllFunc(body, func(declaration []byte) []byte {
		declaration = bytes.ReplaceAll(declaration, []byte("public.apps"), []byte(schema+".apps"))
		return bytes.ReplaceAll(declaration, []byte("public.deployments"), []byte(schema+".deployments"))
	})
}

type scopedMigrationFile struct {
	*bytes.Reader
	info fs.FileInfo
}

func (f *scopedMigrationFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (*scopedMigrationFile) Close() error                 { return nil }

type scopedMigrationInfo struct {
	fs.FileInfo
	size int64
}

func (i scopedMigrationInfo) Size() int64 { return i.size }
