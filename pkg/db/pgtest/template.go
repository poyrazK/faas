package pgtest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/onebox-faas/faas/migrations"
)

// UseTemplateDatabase enables the CI-only database clone harness. It is
// deliberately opt-in so the regular Open contract remains a fresh schema
// on developers' local Postgres instances and in migration-focused tests.
const UseTemplateDatabase = "FAAS_PGTEST_TEMPLATE_DATABASE"

type templateDatabase struct {
	admin *pgxpool.Pool
	base  *pgxpool.Config
	name  string
}

var (
	templateMu    sync.Mutex
	templateByDSN = make(map[string]*templateDatabase)
)

// OpenMigrated returns a fresh database cloned from a database that has
// already run the complete embedded migration set once. Set
// FAAS_PGTEST_TEMPLATE_DATABASE=1 to enable it.
//
// The clone has the same clean, production-shaped public schema as Open's
// migrated result, but avoids replaying hundreds of migrations for every
// state test. Each caller still receives a private database, so tests retain
// isolation and can safely create/drop tables or rows without affecting one
// another. The normal Open helper is used unless the opt-in environment
// variable is set; this keeps migration tests meaningful.
func OpenMigrated(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv(UseTemplateDatabase) == "" {
		return Open(t)
	}
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set; skipping Postgres integration test")
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("pgtest: DATABASE_URL unset; skipping template database integration test")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("pgtest: cannot parse configured DATABASE_URL: %v", err)
	}

	tpl := ensureTemplate(t, dsn, cfg)
	cloneName := "faas_test_clone_" + strconv.Itoa(os.Getpid()) + "_" + randomSuffix(t)
	if _, err := tpl.admin.Exec(context.Background(), fmt.Sprintf(
		"CREATE DATABASE %s TEMPLATE %s", quoteIdentifier(cloneName), quoteIdentifier(tpl.name),
	)); err != nil {
		t.Fatalf("pgtest: create template clone: %v", err)
	}

	cloneCfg := cfg.Copy()
	cloneCfg.ConnConfig.Database = cloneName
	pool, err := pgxpool.NewWithConfig(context.Background(), cloneCfg)
	if err != nil {
		t.Fatalf("pgtest: open template clone: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("pgtest: ping template clone: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		_, _ = tpl.admin.Exec(context.Background(), fmt.Sprintf(
			"DROP DATABASE IF EXISTS %s WITH (FORCE)", quoteIdentifier(cloneName),
		))
	})
	return pool
}

func ensureTemplate(t *testing.T, dsn string, cfg *pgxpool.Config) *templateDatabase {
	t.Helper()

	templateMu.Lock()
	defer templateMu.Unlock()
	if tpl := templateByDSN[dsn]; tpl != nil {
		return tpl
	}

	adminCfg := cfg.Copy()
	adminCfg.ConnConfig.Database = "postgres"
	// A caller-supplied search_path may point at a schema that does not exist
	// in the admin database. The template is intentionally public-shaped.
	delete(adminCfg.ConnConfig.RuntimeParams, "search_path")
	admin, err := pgxpool.NewWithConfig(context.Background(), adminCfg)
	if err != nil {
		t.Fatalf("pgtest: open template admin connection: %v", err)
	}
	if err := admin.Ping(context.Background()); err != nil {
		admin.Close()
		t.Fatalf("pgtest: ping template admin connection: %v", err)
	}

	templateName := "faas_test_template_" + strconv.Itoa(os.Getpid()) + "_" + randomSuffix(t)
	if _, err := admin.Exec(context.Background(), fmt.Sprintf(
		"CREATE DATABASE %s", quoteIdentifier(templateName),
	)); err != nil {
		admin.Close()
		t.Fatalf("pgtest: create migration template: %v", err)
	}

	templateCfg := cfg.Copy()
	templateCfg.ConnConfig.Database = templateName
	delete(templateCfg.ConnConfig.RuntimeParams, "search_path")
	templatePool, err := pgxpool.NewWithConfig(context.Background(), templateCfg)
	if err != nil {
		admin.Close()
		t.Fatalf("pgtest: open migration template: %v", err)
	}
	if err := templatePool.Ping(context.Background()); err != nil {
		templatePool.Close()
		admin.Close()
		t.Fatalf("pgtest: ping migration template: %v", err)
	}
	if err := migrateTemplate(templatePool); err != nil {
		templatePool.Close()
		_, _ = admin.Exec(context.Background(), fmt.Sprintf(
			"DROP DATABASE IF EXISTS %s WITH (FORCE)", quoteIdentifier(templateName),
		))
		admin.Close()
		t.Fatalf("pgtest: migrate template: %v", err)
	}
	// PostgreSQL requires the template database to have no active sessions
	// before it can be used as the source of CREATE DATABASE ... TEMPLATE.
	templatePool.Close()

	tpl := &templateDatabase{admin: admin, base: cfg, name: templateName}
	templateByDSN[dsn] = tpl
	return tpl
}

func migrateTemplate(pool *pgxpool.Pool) error {
	cfg := pool.Config()
	if cfg == nil || cfg.ConnConfig == nil {
		return fmt.Errorf("pool has no config")
	}

	sqlDB, err := sql.Open("pgx", stdlib.RegisterConnConfig(cfg.ConnConfig))
	if err != nil {
		return fmt.Errorf("open stdlib: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.UpContext(context.Background(), sqlDB, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	name := randomSchema(t)
	return strings.TrimPrefix(name, "faas_test_")
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
