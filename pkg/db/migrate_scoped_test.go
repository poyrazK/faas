package db

// adr: 590

import (
	"bytes"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/migrations"
)

func TestIssuedMigrationDeclarationsRespectTargetSchema(t *testing.T) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	scoped := scopedMigrationFS{FS: migrations.FS, schema: `"isolated schema"`}
	for _, entry := range entries {
		if !issuedPublicDeclarationMigration(entry.Name()) {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			original, err := fs.ReadFile(migrations.FS, entry.Name())
			if err != nil {
				t.Fatal(err)
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
			again, _ := fs.ReadFile(migrations.FS, entry.Name())
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
