package datapipeline

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestSourcePrimaryKeyCompatibility(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	for _, test := range []struct {
		name, definition string
		keys             []string
	}{
		{"ordinary", "PRIMARY KEY(id)", []string{"id"}},
		{"included", "PRIMARY KEY(id) INCLUDE(value)", []string{"id"}},
		{"composite_included", "PRIMARY KEY(id,other) INCLUDE(value)", []string{"id", "other"}},
		{"deferred", "PRIMARY KEY(id) DEFERRABLE INITIALLY DEFERRED", nil},
		{"deferrable_immediate", "PRIMARY KEY(id) DEFERRABLE INITIALLY IMMEDIATE", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			name := "inspection_" + test.name
			if _, err := pool.Exec(ctx, "CREATE TABLE "+tableIdent(name)+"(id integer,other integer,value text,"+test.definition+")"); err != nil {
				t.Fatal(err)
			}
			r, table, err := inspectSourceTable(ctx, pool, name)
			if err != nil {
				t.Fatal(err)
			}
			var keys []string
			for _, col := range r.Columns {
				if col.Key {
					keys = append(keys, col.Name)
				}
			}
			if !slices.Equal(keys, test.keys) || table.Supported != (len(test.keys) > 0) {
				t.Fatalf("unexpected key compatibility: keys=%v table=%+v", keys, table)
			}
			if len(test.keys) == 0 && !slices.ContainsFunc(table.Issues, func(issue api.DataConnectionIssue) bool { return issue.Code == "source_primary_key_required" }) {
				t.Fatal("missing actionable replica identity blocker")
			}
			if test.name == "included" {
				// INCLUDE columns can legitimately be NULL at the source. The
				// mirror must preserve that rather than adding them to its key.
				r.Name = "inspection_mirror"
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(ctx) }()
				var owner string
				if err = tx.QueryRow(ctx, `SELECT current_user`).Scan(&owner); err != nil {
					t.Fatal(err)
				}
				p := Pipeline{AccountID: uuid.NewString(), DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 1}}
				if err = createMirrorTable(ctx, p, tx, owner, r); err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(ctx, `INSERT INTO public.inspection_mirror VALUES(1,NULL,NULL)`); err != nil {
					t.Fatal("source-valid NULL was rejected by the mirror", err)
				}
			}
		})
	}
}

func TestPublicationRechecksPrimaryKeyBeforeChangingSource(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE public.inspection_changed(id integer PRIMARY KEY,value text);INSERT INTO public.inspection_changed VALUES(1,'before')`); err != nil {
		t.Fatal(err)
	}
	_, discovered, err := inspectSourceTable(ctx, pool, "inspection_changed")
	if err != nil || !discovered.Supported {
		t.Fatal("initial discovery failed", discovered, err)
	}
	if _, err = pool.Exec(ctx, `ALTER TABLE public.inspection_changed DROP CONSTRAINT inspection_changed_pkey,ADD PRIMARY KEY(id) DEFERRABLE`); err != nil {
		t.Fatal(err)
	}
	p := Pipeline{AccountID: uuid.NewString(), DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 1, Tables: []string{"inspection_changed"}}}
	var invalid *blocked
	if err = ensurePublication(ctx, p, pool); !errors.As(err, &invalid) || invalid.code != "source_primary_key_required" {
		t.Fatal("publication accepted a changed replica identity", err)
	}
	var published bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_publication WHERE pubname=$1)`, pipelineName(p)).Scan(&published); err != nil || published {
		t.Fatal("rejected source still acquired a publication", published, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE public.inspection_changed SET value='after' WHERE id=1;DELETE FROM public.inspection_changed WHERE id=1`); err != nil {
		t.Fatal("pipeline preflight broke source writes", err)
	}
}

func TestSourceRejectsInheritanceBeforePublishing(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE public.inherited_parent(id integer PRIMARY KEY,value text);
 CREATE TABLE public.inherited_child(PRIMARY KEY(id)) INHERITS(public.inherited_parent);
 INSERT INTO public.inherited_child VALUES(1,'before')`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"inherited_parent", "inherited_child"} {
		r, table, err := inspectSourceTable(ctx, pool, name)
		if err != nil || table.Supported || !slices.ContainsFunc(table.Issues, func(issue api.DataConnectionIssue) bool {
			return issue.Code == "source_table_unsupported" && issue.Column == ""
		}) {
			t.Fatal("inheritance was accepted", name, table, err)
		}
		if len(primaryColumns(r)) != 1 {
			t.Fatal("fixture lacks a usable primary key")
		}
		p := Pipeline{AccountID: uuid.NewString(), DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 1, Tables: []string{name}}}
		var invalid *blocked
		if err = ensurePublication(ctx, p, pool); !errors.As(err, &invalid) || invalid.code != "source_table_unsupported" {
			t.Fatal("publication accepted inheritance", err)
		}
		var published bool
		if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_publication WHERE pubname=$1)`, pipelineName(p)).Scan(&published); err != nil || published {
			t.Fatal("rejected hierarchy acquired a publication", published, err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE public.inherited_child SET value='after' WHERE id=1;DELETE FROM public.inherited_child WHERE id=1`); err != nil {
		t.Fatal("rejected pipeline broke unselected source writes", err)
	}
}

func TestPublicationDoesNotEnlistLaterDescendants(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE public.later_parent(id integer PRIMARY KEY,value text)`); err != nil {
		t.Fatal(err)
	}
	p := Pipeline{AccountID: uuid.NewString(), DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 1, Tables: []string{"later_parent"}}}
	if err := ensurePublication(ctx, p, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE public.later_child() INHERITS(public.later_parent);INSERT INTO public.later_child VALUES(1,'before')`); err != nil {
		t.Fatal(err)
	}
	var members []string
	if err := pool.QueryRow(ctx, `SELECT array_agg(tablename::text) FROM pg_publication_tables WHERE pubname=$1`, pipelineName(p)).Scan(&members); err != nil || !slices.Equal(members, p.Tables) {
		t.Fatal("unselected descendant was published", members, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.later_child SET value='after' WHERE id=1;DELETE FROM public.later_child WHERE id=1`); err != nil {
		t.Fatal("unselected descendant writes were broken", err)
	}
	_, inspected, err := inspectSourceTable(ctx, pool, "later_parent")
	if err != nil || inspected.Supported {
		t.Fatal("reinspection accepted a new inheritance hierarchy", inspected, err)
	}
}

func TestSourceRejectsUnloggedTableBeforePublishing(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE UNLOGGED TABLE public.inspection_unlogged(id integer PRIMARY KEY,value text)`); err != nil {
		t.Fatal(err)
	}
	_, discovered, err := inspectSourceTable(ctx, pool, "inspection_unlogged")
	if err != nil || discovered.Supported || !slices.ContainsFunc(discovered.Issues, func(issue api.DataConnectionIssue) bool {
		return issue.Code == "source_table_unsupported" && issue.Remediation != ""
	}) {
		t.Fatal("unlogged table passed discovery", discovered, err)
	}
	p := Pipeline{AccountID: uuid.NewString(), DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 1, Tables: []string{"inspection_unlogged"}}}
	var invalid *blocked
	if err = ensurePublication(ctx, p, pool); !errors.As(err, &invalid) || invalid.code != "source_table_unsupported" {
		t.Fatal("publication did not return the discovery blocker", err)
	}
	var published bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_publication WHERE pubname=$1)`, pipelineName(p)).Scan(&published); err != nil || published {
		t.Fatal("unlogged table acquired a publication", published, err)
	}
}
