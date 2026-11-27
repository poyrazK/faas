package pgextension

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestExtensionsCompatibility(t *testing.T) {
	trgm := Installed{Name: "pg_trgm", Version: "1.5"}
	vector := Installed{Name: "vector", Version: "0.8.0"}
	source := []Installed{trgm, vector}
	if got := ImportBlockers(source, []Installed{{Name: trgm.Name, Version: "1.6"}}, []string{"vector"}); !slices.Equal(got, []Installed{trgm}) {
		t.Fatalf("import blockers = %v", got)
	}
	files := []string{"pg_trgm.control", "pg_trgm--1.5--1.6.sql", "vector--0.8.0.sql"}
	if got := UpgradeBlockers(source, files); !slices.Equal(got, []Installed{vector}) {
		t.Fatalf("upgrade blockers = %v", got)
	}
	if _, ok := EnableSQL(`pg_trgm"; COMMIT; --`); ok {
		t.Fatal("enable accepted an arbitrary extension name")
	}
}

func TestVectorUpgradeRequiresTheSharedLibrary(t *testing.T) {
	vector := Installed{Name: VectorName, Version: VectorVersion}
	installed := []Installed{vector}
	files := []string{"vector.control", "vector--" + VectorVersion + ".sql"}
	if got := UpgradeBlockers(installed, files); !slices.Equal(got, installed) {
		t.Fatalf("missing shared library did not block upgrade: %v", got)
	}
	if got := UpgradeBlockers(installed, append(files, "vector.so")); len(got) != 0 {
		t.Fatalf("complete pgvector image blocked upgrade: %v", got)
	}
}

func TestExtensionsWithPostgreSQL(t *testing.T) {
	dsn := os.Getenv("FAAS_TEST_NATIVE_EXTENSIONS_DSN")
	if dsn == "" {
		t.Skip("set FAAS_TEST_NATIVE_EXTENSIONS_DSN to an empty disposable database")
	}
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	exec := func(sql string) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE ROLE gregale_schema_owner NOLOGIN NOSUPERUSER; CREATE ROLE extension_runtime LOGIN NOSUPERUSER")
	exec("ALTER DATABASE " + pgx.Identifier{conn.Config().Database}.Sanitize() + " OWNER TO gregale_schema_owner")
	exec("REVOKE CREATE ON SCHEMA public FROM PUBLIC; ALTER DEFAULT PRIVILEGES FOR ROLE gregale_schema_owner REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC")
	read := func() Catalog {
		t.Helper()
		var raw []byte
		var catalog Catalog
		if err := conn.QueryRow(ctx, CatalogSQL).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &catalog); err != nil || !catalog.Valid() {
			t.Fatalf("invalid catalog %s: %v", raw, err)
		}
		return catalog
	}
	if before := read(); len(before.Installed) != 0 || len(before.Available) == 0 {
		t.Fatalf("unexpected fresh catalog: %+v", before)
	}
	for _, name := range []string{"pg_trgm", "btree_gist"} {
		enable, _ := EnableSQL(name)
		exec(enable)
		first := read()
		exec(enable)
		again := read()
		if !slices.Equal(first.Installed, again.Installed) || !slices.ContainsFunc(again.Installed, func(i Installed) bool { return i.Name == name }) {
			t.Fatalf("enable did not converge for %s: %+v, %+v", name, first.Installed, again.Installed)
		}
	}
	installed := read().Installed
	requires := append(slices.Clone(installed), Installed{Name: "pg_trgm", Version: "9.999"})
	check, err := ImportCompatibleSQL(requires, nil)
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := conn.QueryRow(ctx, check).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	matching, err := DecodeInstalled(raw)
	if err != nil || !slices.Equal(matching, installed) {
		t.Fatalf("version-aware import check = %v, %v", matching, err)
	}
	exec("SET ROLE extension_runtime")
	var similarity float32
	if err := conn.QueryRow(ctx, "SELECT public.similarity('gregale', 'gregale')").Scan(&similarity); err != nil || similarity != 1 {
		t.Fatalf("similarity = %v, %v", similarity, err)
	}
}
