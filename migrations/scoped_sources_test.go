package migrations

// adr: 595

import (
	"bytes"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestIssuedMigrationDeclarationsRespectTargetSchema(t *testing.T) {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	scoped := SourcesForSchema("isolated schema")
	for _, entry := range entries {
		if !issuedPublicDeclarationMigration(entry.Name()) {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			original, err := fs.ReadFile(FS, entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			public, err := fs.ReadFile(SourcesForSchema("public"), entry.Name())
			if err != nil || !bytes.Equal(public, original) {
				t.Fatal("public execution changed issued migration bytes", err)
			}
			body, err := fs.ReadFile(scoped, entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(original, body) || bytes.Contains(body, []byte("public.")) {
				t.Fatal("issued declaration escaped the target schema")
			}
			info, err := fs.Stat(scoped, entry.Name())
			if err != nil || info.Size() != int64(len(body)) {
				t.Fatal("adapted file has incorrect size", err)
			}
			again, _ := fs.ReadFile(FS, entry.Name())
			if !bytes.Equal(original, again) {
				t.Fatal("embedded migration was modified")
			}
		})
	}
}

func TestMigrationScopingPreservesOtherSQL(t *testing.T) {
	const sql = "CREATE FUNCTION public.unrelated() RETURNS void AS $$ public.apps $$ LANGUAGE plpgsql;\n"
	scoped := scopedMigrationFS{FS: fstest.MapFS{"unknown.sql": &fstest.MapFile{Data: []byte(sql)}}, schema: `"test"`}
	body, err := fs.ReadFile(scoped, "unknown.sql")
	if err != nil || string(body) != sql {
		t.Fatal("unissued migration was adapted", err)
	}
	const protected = "-- CREATE FUNCTION public.application_standard_comment()\nSELECT 'public.apps';\n" + sql
	if got := scopeIssuedMigrationDeclarations([]byte(protected), `"test"`); string(got) != protected {
		t.Fatal("SQL outside the frozen declarations was adapted")
	}
}

func TestMigrationScopingPreservesAlreadyUnqualifiedIssuedSQL(t *testing.T) {
	// This issued migration already uses search_path-relative declarations.
	// Requiring its bytes to change falsely reported a schema escape. Keep
	// it outside the narrowly scoped public-declaration adaptation list.
	const name = "20261003160458628_application_standard_runtime_default_base.sql"
	original, err := fs.ReadFile(FS, name)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(original, []byte("public.")) || issuedPublicDeclarationMigration(name) {
		t.Fatal("unqualified migration unexpectedly requires public adaptation")
	}
	for _, schema := range []string{"public", "isolated schema", `schema"with quote`} {
		body, err := fs.ReadFile(SourcesForSchema(schema), name)
		if err != nil || !bytes.Equal(body, original) {
			t.Fatalf("%q changed already scoped SQL: %v", schema, err)
		}
	}
}
