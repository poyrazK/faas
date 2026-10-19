#!/usr/bin/env python3
"""Unit tests for check_migration_expand_contract.py (expand/contract linter)."""
import contextlib
import importlib.util
import io
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

spec = importlib.util.spec_from_file_location(
    "expand_contract", pathlib.Path(__file__).with_name("check_migration_expand_contract.py"))
ec = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = ec  # dataclasses resolve annotations through sys.modules
spec.loader.exec_module(ec)

CATALOGUE = ec.Catalogue(relations={"apps", "events", "app_summary", "orgs", "projects", "accounts"},
                         indexes={"events_at_idx", "apps_slug_key"}, unique={"apps_slug_key", "apps_pkey"},
                         index_tables={"events_at_idx": "events", "apps_slug_key": "apps"},
                         unique_keys={"apps": [frozenset({"id"}), frozenset({"account_id", "slug"})]})


def no_refs(name, table):
    return "", []


def refs_in_release(name, table):
    return "the previous release (v1)", [f"pkg/state/{name}.sql"]


def lint(up, referenced=no_refs, header="", down=""):
    text = f"{header}-- +goose Up\n{up}\n-- +goose Down\n{down}\n"
    return sorted((f.rule, f.target) for f in ec.lint_migration("migrations/1_x.sql", text, CATALOGUE, referenced))


LT = "SET LOCAL lock_timeout = '5s';\n"


class LockTimeoutTest(unittest.TestCase):
    def test_alter_existing_table_needs_lock_timeout(self):
        self.assertEqual(lint("ALTER TABLE apps ADD COLUMN note text;"), [("lock-timeout", "apps")])

    def test_lock_timeout_before_satisfies(self):
        self.assertEqual(lint(LT + "ALTER TABLE apps ADD COLUMN note text;"), [])

    def test_one_lock_timeout_finding_per_table(self):
        up = "ALTER TABLE apps ADD COLUMN a text;\nALTER TABLE apps ADD COLUMN b text;\nALTER TABLE events ADD COLUMN c text;"
        self.assertEqual(lint(up), [("lock-timeout", "apps"), ("lock-timeout", "events")])

    def test_lock_timeout_after_does_not(self):
        self.assertEqual(lint("ALTER TABLE apps ADD COLUMN note text;\n" + LT), [("lock-timeout", "apps")])

    def test_set_config_in_do_block(self):
        up = "DO $$ BEGIN PERFORM set_config('lock_timeout', '2s', true); END $$;\nALTER TABLE public.apps ADD COLUMN note text;"
        self.assertEqual(lint(up), [])

    def test_lock_timeout_bounds(self):
        self.assertEqual(lint("SET lock_timeout = 0;\nALTER TABLE apps ADD COLUMN note text;"),
                         [("lock-timeout", "*"), ("lock-timeout", "apps")])
        self.assertEqual(lint("SET lock_timeout TO '1min';"), [("lock-timeout", "*")])
        self.assertEqual(lint("SET lock_timeout TO 3000;\nALTER TABLE apps ADD COLUMN note text;"), [])

    def test_new_table_is_exempt(self):
        up = "CREATE TABLE widgets (id uuid PRIMARY KEY);\nALTER TABLE widgets ADD COLUMN note text NOT NULL;\nCREATE INDEX widgets_note ON widgets (note);"
        self.assertEqual(lint(up), [])

    def test_table_missing_from_base_catalogue_is_exempt(self):
        # Created by another migration in the same change.
        self.assertEqual(lint("ALTER TABLE gadgets ADD COLUMN note text;"), [])

    def test_alter_inside_do_block(self):
        up = """DO $r$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='x') THEN
  ALTER TABLE apps ADD CONSTRAINT x CHECK(note <> '') NOT VALID;
 END IF;
END $r$;"""
        self.assertEqual(lint(up), [("lock-timeout", "apps")])

    def test_validate_constraint_alone_is_not_blocking(self):
        self.assertEqual(lint("ALTER TABLE apps VALIDATE CONSTRAINT x;"), [])

    def test_trigger_and_view_replacement(self):
        self.assertEqual(lint("CREATE TRIGGER t AFTER INSERT ON apps FOR EACH ROW EXECUTE FUNCTION f();"), [("lock-timeout", "apps")])
        self.assertEqual(lint("DROP TRIGGER IF EXISTS t ON apps;"), [("lock-timeout", "apps")])
        self.assertEqual(lint("CREATE OR REPLACE VIEW app_summary AS SELECT 1;"), [("lock-timeout", "app_summary")])

    def test_drop_existing_index(self):
        self.assertEqual(lint("DROP INDEX IF EXISTS events_at_idx;"), [("lock-timeout", "events_at_idx")])
        self.assertEqual(lint("DROP INDEX IF EXISTS brand_new_idx;"), [])

    def test_function_bodies_are_not_executed(self):
        up = "CREATE OR REPLACE FUNCTION f() RETURNS trigger AS $$ BEGIN ALTER TABLE apps ADD COLUMN x int; RETURN NEW; END $$ LANGUAGE plpgsql;"
        self.assertEqual(lint(up), [])

    def test_down_section_is_ignored(self):
        self.assertEqual(lint("SELECT 1;", down="ALTER TABLE apps DROP COLUMN note;\nDROP TABLE apps;", referenced=refs_in_release), [])

    def test_comments_and_strings_do_not_confuse(self):
        up = "-- ALTER TABLE apps DROP COLUMN x;\n/* DROP TABLE apps; */\nINSERT INTO events(kind) VALUES ('ALTER TABLE apps; DROP TABLE apps');"
        self.assertEqual(lint(up, referenced=refs_in_release), [])


class IndexTest(unittest.TestCase):
    def test_plain_index_on_existing_table(self):
        self.assertEqual(lint(LT + "CREATE INDEX IF NOT EXISTS events_kind ON events (kind);"), [("index-build", "events")])

    def test_concurrently_needs_no_transaction(self):
        self.assertEqual(lint("CREATE INDEX CONCURRENTLY IF NOT EXISTS events_kind ON events (kind);"),
                         [("concurrently-in-transaction", "events_kind")])
        self.assertEqual(lint("CREATE INDEX CONCURRENTLY IF NOT EXISTS events_kind ON events (kind);",
                              header="-- +goose NO TRANSACTION\n"), [])

    def test_add_primary_key_builds_an_index(self):
        self.assertEqual(lint(LT + "ALTER TABLE apps ADD PRIMARY KEY (id);"), [("index-build", "apps")])
        self.assertEqual(lint(LT + "ALTER TABLE apps ADD CONSTRAINT apps_pk PRIMARY KEY USING INDEX apps_id_idx;"), [])

    def test_waiver(self):
        up = "-- migration-lint: allow index-build events: 1 s write pause measured on 394k rows\n" + LT + \
            "CREATE INDEX events_kind ON events (kind);"
        self.assertEqual(lint(up), [])

    def test_waiver_needs_a_reason(self):
        up = "-- migration-lint: allow index-build events\n" + LT + "CREATE INDEX events_kind ON events (kind);"
        self.assertEqual(lint(up), [("index-build", "events")])


class NotNullTest(unittest.TestCase):
    def test_add_not_null_without_default(self):
        self.assertEqual(lint(LT + "ALTER TABLE apps ADD COLUMN IF NOT EXISTS tier text NOT NULL;"), [("not-null", "apps.tier")])

    def test_add_not_null_with_default(self):
        self.assertEqual(lint(LT + "ALTER TABLE apps ADD COLUMN tier text NOT NULL DEFAULT 'free';"), [])

    def test_check_is_not_null_is_not_a_not_null_column(self):
        # No not-null finding; the column CHECK itself validates every row.
        self.assertEqual(lint(LT + "ALTER TABLE apps ADD COLUMN tier text CHECK (tier IS NOT NULL OR true);"),
                         [("constraint-validation", "apps.tier")])

    def test_set_not_null(self):
        self.assertEqual(lint(LT + "ALTER TABLE apps ALTER COLUMN tier SET NOT NULL;"), [("not-null", "apps.tier")])
        self.assertEqual(lint(LT + "ALTER TABLE apps ALTER COLUMN tier SET DEFAULT 'free', ALTER COLUMN tier SET NOT NULL;"), [])

    def test_volatile_default_rewrites(self):
        self.assertEqual(lint(LT + "ALTER TABLE apps ADD COLUMN seen_at timestamptz NOT NULL DEFAULT clock_timestamp();"),
                         [("table-rewrite", "apps.seen_at")])
        self.assertEqual(lint(LT + "ALTER TABLE apps ADD COLUMN seen_at timestamptz NOT NULL DEFAULT now();"), [])


class ContractTest(unittest.TestCase):
    def test_column_type_change(self):
        self.assertEqual(lint(LT + "ALTER TABLE apps ALTER COLUMN ram_mb TYPE bigint;"), [("column-type", "apps.ram_mb")])
        self.assertEqual(lint(LT + "ALTER TABLE apps ALTER COLUMN ram_mb SET DATA TYPE bigint;"), [("column-type", "apps.ram_mb")])

    def test_column_named_type_is_not_a_type_change(self):
        self.assertEqual(lint(LT + "ALTER TABLE apps ALTER COLUMN type SET DEFAULT 'web';"), [])

    def test_drop_column_still_read(self):
        self.assertEqual(lint(LT + "ALTER TABLE apps DROP COLUMN IF EXISTS legacy;", referenced=refs_in_release),
                         [("drop-column", "apps.legacy")])
        self.assertEqual(lint(LT + "ALTER TABLE apps DROP COLUMN legacy, DROP COLUMN old;", referenced=refs_in_release),
                         [("drop-column", "apps.legacy"), ("drop-column", "apps.old")])

    def test_drop_column_nobody_reads(self):
        self.assertEqual(lint(LT + "ALTER TABLE apps DROP COLUMN legacy;"), [])

    def test_drop_constraint_is_fine(self):
        self.assertEqual(lint(LT + "ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_check;", referenced=refs_in_release), [])

    def test_drop_table(self):
        self.assertEqual(lint(LT + "DROP TABLE IF EXISTS public.events CASCADE;", referenced=refs_in_release), [("drop-table", "events")])
        self.assertEqual(lint(LT + "DROP TABLE IF EXISTS scratch;", referenced=refs_in_release), [])

    def test_view_redefinition_is_not_a_drop(self):
        up = LT + "DROP VIEW IF EXISTS app_summary;\nCREATE VIEW app_summary AS SELECT 2;"
        self.assertEqual(lint(up, referenced=refs_in_release), [])

    def test_renames(self):
        self.assertEqual(lint(LT + "ALTER TABLE apps RENAME COLUMN ram TO ram_mb;", referenced=refs_in_release), [("rename-column", "apps.ram")])
        self.assertEqual(lint(LT + "ALTER TABLE apps RENAME ram TO ram_mb;", referenced=refs_in_release), [("rename-column", "apps.ram")])
        self.assertEqual(lint(LT + "ALTER TABLE apps RENAME TO applications;", referenced=refs_in_release), [("rename-table", "apps")])
        self.assertEqual(lint(LT + "ALTER TABLE apps RENAME CONSTRAINT a TO b;", referenced=refs_in_release), [])

    def test_waiver_by_table_prefix(self):
        up = "-- migration-lint: allow drop-column apps: legacy is only written, never read, since rc.250\n" + LT + \
            "ALTER TABLE apps DROP COLUMN legacy;"
        self.assertEqual(lint(up, referenced=refs_in_release), [])


NO_TX = "-- +goose NO TRANSACTION\n"
# The batched backfill docs/release-compatibility.md recommends.
BACKFILL = """DO $backfill$
DECLARE
  last_id uuid := '00000000-0000-0000-0000-000000000000';
BEGIN
  LOOP
    PERFORM set_config('lock_timeout', '5s', true);
    WITH batch AS (
      SELECT id FROM apps WHERE id > last_id ORDER BY id LIMIT 1000
    ), moved AS (
      UPDATE apps SET org_id = apps.account_id FROM batch WHERE apps.id = batch.id AND apps.org_id IS NULL
    )
    SELECT max(id) INTO last_id FROM batch;
    EXIT WHEN last_id IS NULL;
    COMMIT;
  END LOOP;
END
$backfill$;"""


class NoDowntimeTest(unittest.TestCase):
    """The rules that keep a migration from needing writers stopped
    (ADR-202610171900, amendment 1). None can be waived."""

    CASES = [
        # name, header, Up section, findings
        # constraint-validation
        ("foreign key validated under the lock", "",
         LT + "ALTER TABLE apps ADD CONSTRAINT apps_org_fk FOREIGN KEY (org_id) REFERENCES orgs (id);",
         [("constraint-validation", "apps.apps_org_fk")]),
        ("foreign key NOT VALID", "",
         LT + "ALTER TABLE apps ADD CONSTRAINT apps_org_fk FOREIGN KEY (org_id) REFERENCES public.orgs (id) NOT VALID;", []),
        ("check validated under the lock", "", LT + "ALTER TABLE apps ADD CONSTRAINT apps_tier_chk CHECK (tier <> '');",
         [("constraint-validation", "apps.apps_tier_chk")]),
        ("check NOT VALID", "", LT + "ALTER TABLE apps ADD CONSTRAINT apps_tier_chk CHECK (tier <> '') NOT VALID;", []),
        ("unnamed constraints", "", LT + "ALTER TABLE apps ADD CHECK (ram_mb > 0), ADD FOREIGN KEY (org_id) REFERENCES orgs;",
         [("constraint-validation", "apps"), ("constraint-validation", "apps")]),
        ("quoted name", "", LT + "ALTER TABLE apps ADD CONSTRAINT \"Apps_Tier\" CHECK (tier <> '');",
         [("constraint-validation", "apps.Apps_Tier")]),
        ("validate on its own", "", "ALTER TABLE apps VALIDATE CONSTRAINT apps_tier_chk;", []),
        ("validate in the migration that adds it", "",
         LT + "ALTER TABLE apps ADD CONSTRAINT apps_tier_chk CHECK (tier <> '') NOT VALID;\n"
              "ALTER TABLE apps VALIDATE CONSTRAINT apps_tier_chk;",
         [("constraint-validation", "apps.apps_tier_chk")]),
        ("column CHECK", "", LT + "ALTER TABLE apps ADD COLUMN tier text NOT NULL DEFAULT 'free' CHECK (tier <> '');",
         [("constraint-validation", "apps.tier")]),
        ("column REFERENCES without a default is not validated", "",
         LT + "ALTER TABLE apps ADD COLUMN org_id uuid REFERENCES orgs (id);", []),
        ("column REFERENCES with a default", "",
         LT + "ALTER TABLE apps ADD COLUMN org_id uuid DEFAULT '00000000-0000-0000-0000-000000000000' REFERENCES orgs (id);",
         [("constraint-validation", "apps.org_id")]),
        ("new table is exempt", "",
         "CREATE TABLE widgets (id int, n int);\nALTER TABLE widgets ADD CONSTRAINT w_n CHECK (n > 0);\n"
         "ALTER TABLE widgets DROP CONSTRAINT w_n, ADD CONSTRAINT w_n CHECK (n >= 0);\n"
         "UPDATE widgets SET n = 1;", []),
        ("a new table's foreign key locks the existing parent", "", "CREATE TABLE widgets (app_id uuid REFERENCES apps (id));",
         [("lock-timeout", "apps")]),
        ("so does one added after the CREATE", "",
         "CREATE TABLE widgets (app_id uuid);\nALTER TABLE widgets ADD CONSTRAINT w_app FOREIGN KEY (app_id) REFERENCES apps (id);",
         [("lock-timeout", "apps")]),
        ("not waivable", "",
         "-- migration-lint: allow constraint-validation apps: 40 rows\n" + LT +
         "ALTER TABLE apps ADD CONSTRAINT apps_tier_chk CHECK (tier <> '');",
         [("constraint-validation", "apps.apps_tier_chk")]),
        # constraint-churn
        ("drop and re-add a foreign key", "",
         LT + "ALTER TABLE apps DROP CONSTRAINT apps_account_id_fkey;\n"
              "ALTER TABLE apps ADD CONSTRAINT apps_account_id_fkey FOREIGN KEY (account_id) REFERENCES orgs (id) NOT VALID;",
         [("constraint-churn", "apps")]),
        ("drop and add in one statement", "",
         LT + "ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_tier_chk, ADD CONSTRAINT apps_tier_chk CHECK (tier <> '') NOT VALID;",
         [("constraint-churn", "apps")]),
        ("add then drop the old one", "",
         LT + "ALTER TABLE apps ADD CONSTRAINT apps_tier_v2 CHECK (tier <> '') NOT VALID;\n"
              "ALTER TABLE apps DROP CONSTRAINT apps_tier_chk;",
         [("constraint-churn", "apps")]),
        ("drop on one table, add on another", "",
         LT + "ALTER TABLE apps DROP CONSTRAINT apps_tier_chk;\n"
              "ALTER TABLE events ADD CONSTRAINT events_kind_chk CHECK (kind <> '') NOT VALID;", []),
        ("primary key swapped to a concurrently built index", "",
         LT + "ALTER TABLE apps DROP CONSTRAINT apps_pkey, ADD CONSTRAINT apps_pkey PRIMARY KEY USING INDEX apps_pkey_v2;", []),
        # unbatched-write
        ("bulk update", "", "UPDATE apps SET tier = 'free' WHERE tier IS NULL;", [("unbatched-write", "apps")]),
        ("update without WHERE", "", "UPDATE ONLY public.apps SET tier = 'free';", [("unbatched-write", "apps")]),
        ("bulk delete", "", "DELETE FROM events WHERE at < now() - interval '30 days';", [("unbatched-write", "events")]),
        ("one bounded batch", "",
         "UPDATE apps SET tier = 'free' WHERE id IN (SELECT id FROM apps WHERE tier IS NULL ORDER BY id LIMIT 1000);", []),
        ("batch too large", "", "DELETE FROM events WHERE id IN (SELECT id FROM events LIMIT 50000);",
         [("unbatched-write", "events")]),
        ("LIMIT ALL", "", "DELETE FROM events WHERE id IN (SELECT id FROM events LIMIT ALL);", [("unbatched-write", "events")]),
        ("LIMIT on another table", "",
         "UPDATE apps SET tier = 'free' WHERE account_id IN (SELECT id FROM accounts ORDER BY id LIMIT 100);",
         [("unbatched-write", "apps")]),
        ("LIMIT only in SET", "", "UPDATE apps SET tier = (SELECT tier FROM apps LIMIT 1);", [("unbatched-write", "apps")]),
        ("batch from a FROM subquery", "",
         "UPDATE apps SET tier = 'free' FROM (SELECT id FROM apps WHERE id > $1 ORDER BY id LIMIT 500) b WHERE apps.id = b.id;", []),
        ("batch from DELETE USING", "",
         "DELETE FROM events USING (SELECT id FROM events ORDER BY id LIMIT 500) b WHERE events.id = b.id;", []),
        ("batch from a WITH query", "",
         "WITH b AS (SELECT id FROM apps ORDER BY id LIMIT 500) UPDATE apps SET tier = 'free' FROM b WHERE apps.id = b.id;", []),
        ("unbounded data-modifying WITH query", "",
         "WITH gone AS (DELETE FROM events WHERE at < now() RETURNING id) SELECT count(*) FROM gone;",
         [("unbatched-write", "events")]),
        ("variable batch size", "",
         "DO $$ DECLARE n int := 500; BEGIN DELETE FROM events WHERE id IN (SELECT id FROM events LIMIT n); END $$;", []),
        ("WHERE CURRENT OF", "", "UPDATE apps SET tier = 'free' WHERE CURRENT OF app_cursor;", []),
        ("pinned to the primary key", "",
         "UPDATE apps SET tier = 'free' WHERE id = '6f1c6a52-0000-4000-8000-000000000001'::uuid RETURNING id;", []),
        ("pinned to a unique key", "", "UPDATE apps SET tier = 'free' WHERE account_id = 'a' AND apps.slug = 'web';", []),
        ("a short list of keys", "", "DELETE FROM apps WHERE id IN ('a', 'b', 'c');", []),
        ("part of a unique key", "", "UPDATE apps SET tier = 'free' WHERE slug = 'web';", [("unbatched-write", "apps")]),
        ("a key or anything", "", "UPDATE apps SET tier = 'free' WHERE id = 'a' OR tier IS NULL;", [("unbatched-write", "apps")]),
        ("a key from a variable", "", "DO $$ DECLARE r record; BEGIN UPDATE apps SET tier = 'free' WHERE id = r.id; END $$;",
         [("unbatched-write", "apps")]),
        ("a key from a subquery", "", "DELETE FROM apps WHERE id IN (SELECT id FROM apps);", [("unbatched-write", "apps")]),
        ("the documented backfill", NO_TX, BACKFILL, []),
        ("backfill loop inside one transaction", "", BACKFILL, [("unbatched-write", "apps")]),
        ("backfill loop that never commits", NO_TX, BACKFILL.replace("COMMIT;", ""), [("unbatched-write", "apps")]),
        ("row-by-row loop", NO_TX,
         "DO $$ DECLARE r record; BEGIN FOR r IN SELECT id FROM apps LOOP UPDATE apps SET tier = 'free' WHERE id = r.id; "
         "COMMIT; END LOOP; END $$;", [("unbatched-write", "apps")]),
        ("SELECT FOR UPDATE is not a write", "", "WITH x AS (SELECT 1) SELECT id FROM apps FOR UPDATE;", []),
        ("inserts are not checked", "", "INSERT INTO events (kind) SELECT 'x' FROM apps;", []),
        ("not waivable", "", "-- migration-lint: allow unbatched-write apps: 12 rows in production\n"
                             "UPDATE apps SET tier = 'free';", [("unbatched-write", "apps")]),
        # lock-fanout
        ("three existing tables", "",
         LT + "ALTER TABLE apps ADD COLUMN a int;\nALTER TABLE events ADD COLUMN b int;\nALTER TABLE orgs ADD COLUMN c int;", []),
        ("four existing tables", "",
         LT + "ALTER TABLE apps ADD COLUMN a int;\nALTER TABLE events ADD COLUMN b int;\nALTER TABLE orgs ADD COLUMN c int;\n"
              "CREATE TRIGGER t AFTER INSERT ON projects FOR EACH ROW EXECUTE FUNCTION f();",
         [("lock-fanout", "projects")]),
        ("a foreign key locks the table it references", "",
         LT + "ALTER TABLE apps ADD CONSTRAINT apps_org_fk FOREIGN KEY (org_id) REFERENCES orgs (id) NOT VALID;\n"
              "ALTER TABLE events ADD CONSTRAINT events_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) NOT VALID;",
         [("lock-fanout", "projects")]),
        # Three tables, not four: the index counts as the table it is on.
        ("a dropped index locks its table", "",
         LT + "DROP INDEX events_at_idx;\nALTER TABLE events ADD COLUMN a int;\nALTER TABLE apps ADD COLUMN b int;\n"
              "ALTER TABLE orgs ADD COLUMN c int;", []),
        ("validation does not block writes", "",
         "ALTER TABLE apps VALIDATE CONSTRAINT a;\nALTER TABLE events VALIDATE CONSTRAINT b;\n"
         "ALTER TABLE orgs VALIDATE CONSTRAINT c;\nALTER TABLE projects VALIDATE CONSTRAINT d;", []),
        ("each NO TRANSACTION statement commits on its own", NO_TX,
         "\n".join(f"DO $$ BEGIN PERFORM set_config('lock_timeout', '5s', true); ALTER TABLE {t} ADD COLUMN n int; END $$;"
                   for t in ("apps", "events", "orgs", "projects")), []),
        ("one StatementBegin block is one transaction", NO_TX,
         "-- +goose StatementBegin\n" + LT + "ALTER TABLE apps ADD COLUMN a int;\nALTER TABLE events ADD COLUMN b int;\n"
         "ALTER TABLE orgs ADD COLUMN c int;\nALTER TABLE projects ADD COLUMN d int;\n-- +goose StatementEnd",
         [("lock-fanout", "projects")]),
        ("not waivable", "",
         "-- migration-lint: allow lock-fanout *: tables are small\n" + LT +
         "LOCK TABLE apps, events, orgs, projects IN SHARE ROW EXCLUSIVE MODE;", [("lock-fanout", "projects")]),
        # lock_timeout under NO TRANSACTION
        ("SET LOCAL as its own statement does nothing", NO_TX, LT + "ALTER TABLE apps ADD COLUMN n int;",
         [("lock-timeout", "apps")]),
        ("session SET may run on another pooled connection", NO_TX,
         "SET lock_timeout = '5s';\nALTER TABLE apps ADD COLUMN n int;", [("lock-timeout", "apps")]),
        ("lock_timeout in the same StatementBegin block", NO_TX,
         "-- +goose StatementBegin\n" + LT + "ALTER TABLE apps ADD COLUMN n int;\n-- +goose StatementEnd", []),
        ("lock_timeout set before a COMMIT does not survive it", NO_TX,
         "DO $$ BEGIN PERFORM set_config('lock_timeout', '5s', true); COMMIT; ALTER TABLE apps ADD COLUMN n int; END $$;",
         [("lock-timeout", "apps")]),
        ("TRUNCATE takes a strong lock", "", "TRUNCATE events;", [("lock-timeout", "events")]),
    ]

    def test_cases(self):
        for name, header, up, want in self.CASES:
            with self.subTest(name):
                self.assertEqual(lint(up, header=header), sorted(want))

    def test_the_documented_backfill_passes(self):
        doc = pathlib.Path(__file__).parents[2].joinpath("docs", "release-compatibility.md").read_text(encoding="utf-8")
        section = doc[doc.index("### Batched backfill"):]
        text = section[section.index("```sql\n") + 7 : section.index("\n```", section.index("```sql\n"))] + "\n"
        findings = ec.lint_migration("migrations/1_x.sql", text, CATALOGUE, no_refs)
        findings += ec.check_compat_marker("migrations/1_x.sql", text, CATALOGUE)
        self.assertEqual(findings, [])
        self.assertIn("UPDATE apps", text)

    def test_the_rc277_cutover_shape_fails_every_rule(self):
        # The shape of 20261018003100003: ownership foreign keys dropped and
        # re-added, validated under the lock, across many hot tables, with a
        # bulk backfill, in one transaction.
        up = LT + "UPDATE apps SET org_id = account_id WHERE org_id IS NULL;\n" + "\n".join(
            f"ALTER TABLE {t} DROP CONSTRAINT {t}_account_id_fkey;\n"
            f"ALTER TABLE {t} ADD CONSTRAINT {t}_account_id_fkey FOREIGN KEY (account_id) REFERENCES orgs (id);"
            for t in ("apps", "events", "projects"))
        self.assertEqual(sorted({rule for rule, _ in lint(up)}),
                         ["constraint-churn", "constraint-validation", "lock-fanout", "unbatched-write"])


class CatalogueTest(unittest.TestCase):
    def test_load_catalogue(self):
        cat = ec.load_catalogue("CREATE TABLE public.apps (\nCREATE VIEW public.app_summary AS\n"
                                "CREATE MATERIALIZED VIEW public.mv AS\nCREATE UNIQUE INDEX apps_slug ON public.apps USING btree (slug);\n"
                                "CREATE INDEX mv_at ON ONLY public.mv USING btree (at);\n")
        self.assertEqual(cat.relations, {"apps", "app_summary", "mv"})
        self.assertEqual(cat.indexes, {"apps_slug", "mv_at"})
        self.assertEqual(cat.unique, {"apps_slug"})
        self.assertEqual(cat.index_tables, {"apps_slug": "apps", "mv_at": "mv"})

    def test_unique_keys(self):
        cat = ec.load_catalogue("ALTER TABLE ONLY public.apps\n    ADD CONSTRAINT apps_pkey PRIMARY KEY (id);\n"
                                "ALTER TABLE ONLY public.apps\n    ADD CONSTRAINT apps_name_key UNIQUE (account_id, name);\n"
                                "CREATE UNIQUE INDEX apps_slug ON public.apps USING btree (slug);\n"
                                "CREATE UNIQUE INDEX apps_live ON public.apps USING btree (host) WHERE (deleted_at IS NULL);\n"
                                "CREATE UNIQUE INDEX apps_email ON public.apps USING btree (lower(email));\n")
        self.assertEqual(cat.unique_keys, {"apps": [frozenset({"id"}), frozenset({"account_id", "name"}), frozenset({"slug"})]})

    def test_unique_constraints_are_on_conflict_targets(self):
        cat = ec.load_catalogue("ALTER TABLE ONLY public.apps\n    ADD CONSTRAINT apps_pkey PRIMARY KEY (id);\n"
                                "ALTER TABLE ONLY public.apps\n    ADD CONSTRAINT apps_name_key UNIQUE (name);\n"
                                "ALTER TABLE ONLY public.apps\n    ADD CONSTRAINT apps_tier_chk CHECK (tier <> '');\n"
                                "CREATE INDEX apps_tier ON public.apps USING btree (tier);\n")
        self.assertEqual(cat.unique, {"apps_pkey", "apps_name_key"})


class GitTest(unittest.TestCase):
    """End to end: a release tag still reads a column the change drops."""

    def git(self, *args):
        env = dict(os.environ, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@example.com",
                   GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@example.com")
        subprocess.run(["git", "-C", self.repo, *args], check=True, capture_output=True, env=env)

    def write(self, path, text):
        full = os.path.join(self.repo, path)
        os.makedirs(os.path.dirname(full), exist_ok=True)
        with open(full, "w", encoding="utf-8") as fh:
            fh.write(text)

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.repo = self.tmp.name
        self.git("init", "-q", "-b", "main")
        self.write("schema.sql", "CREATE TABLE public.apps (\n);\n")
        self.write("pkg/state/apps.sql", "-- name: GetApp :one\nSELECT id, legacy_tier FROM apps WHERE id = $1;\n")
        self.write("pkg/state/apps_test.go", "package state // apps legacy_tier\n")
        self.git("add", ".")
        self.git("commit", "-q", "-m", "release")
        self.git("tag", "v0.1.0")
        # The base stops reading the column; the release still does.
        self.write("pkg/state/apps.sql", "-- name: GetApp :one\nSELECT id FROM apps WHERE id = $1;\n")
        self.git("commit", "-q", "-am", "stop reading legacy_tier")

    def tearDown(self):
        self.tmp.cleanup()

    def run_main(self, compat="contract"):
        self.write("migrations/20261017000000001_drop_legacy_tier.sql",
                   f"-- migration-compat: {compat}\n-- +goose Up\nSET LOCAL lock_timeout = '5s';\n"
                   "ALTER TABLE apps DROP COLUMN legacy_tier;\n-- +goose Down\nSELECT 1;\n")
        return self.main()

    def main(self):
        # Keep ::error lines out of the CI log, where they become annotations.
        with contextlib.redirect_stdout(io.StringIO()):
            return ec.main(["--repo", self.repo, "--base", "HEAD"])

    def test_drop_fails_while_the_release_reads_it(self):
        self.assertEqual(self.run_main(), 1)

    def test_drop_passes_once_a_release_no_longer_reads_it(self):
        self.git("tag", "v0.2.0")
        self.assertEqual(self.run_main(), 0)

    def test_no_new_migrations(self):
        self.assertEqual(self.main(), 0)

    def test_a_drop_marked_expand_fails(self):
        self.git("tag", "v0.2.0")
        self.assertEqual(self.run_main(compat="expand"), 1)

    def test_a_migration_without_a_class_fails(self):
        self.write("migrations/20261017000000002_note.sql", "-- +goose Up\nCREATE TABLE notes (id int);\n")
        self.assertEqual(self.main(), 1)


def marked(up, compat="expand", header=""):
    return f"-- migration-compat: {compat}\n{header}-- +goose Up\n{up}\n-- +goose Down\nSELECT 1;\n"


def marker_findings(text):
    return sorted((f.rule, f.target) for f in ec.check_compat_marker("migrations/1_x.sql", text, CATALOGUE))


class CompatMarkerTest(unittest.TestCase):
    """The PR-time check: every new migration declares its class, and an
    expand migration makes no contract change."""

    CASES = [
        # name, migration text, compat-marker findings
        ("expand, additive", marked(LT + "ALTER TABLE apps ADD COLUMN note text;"), []),
        ("contract may drop", marked(LT + "ALTER TABLE apps DROP COLUMN legacy;", "contract"), []),
        ("no marker", "-- +goose Up\nCREATE TABLE notes (id int);\n", [("compat-marker", "*")]),
        ("two markers", "-- migration-compat: expand\n" + marked("SELECT 1;"), [("compat-marker", "*")]),
        ("unknown class", marked("SELECT 1;", "additive"), [("compat-marker", "*")]),
        ("expand drops a column nobody reads", marked(LT + "ALTER TABLE apps DROP COLUMN legacy;"),
         [("compat-marker", "apps.legacy")]),
        ("expand renames", marked(LT + "ALTER TABLE apps RENAME COLUMN ram TO ram_mb;"), [("compat-marker", "apps.ram")]),
        ("expand changes a type", marked(LT + "ALTER TABLE apps ALTER COLUMN ram_mb TYPE bigint;"),
         [("compat-marker", "apps.ram_mb")]),
        ("expand waives not-null",
         marked("-- migration-lint: allow not-null apps.tier: every writer sets it since rc.250\n" + LT +
                "ALTER TABLE apps ALTER COLUMN tier SET NOT NULL;"), [("compat-marker", "apps.tier")]),
        ("expand waives lock-timeout",
         marked("-- migration-lint: allow lock-timeout apps: tiny table\nALTER TABLE apps ADD COLUMN note text;"),
         [("compat-marker", "apps")]),
        ("expand with a measured index-build waiver",
         marked("-- migration-lint: allow index-build events: ~1 s write pause on 394k rows\n" + LT +
                "CREATE INDEX events_kind ON events (kind);"), []),
        ("expand drops a function", marked("DROP FUNCTION IF EXISTS old_fn(int);"), [("compat-marker", "old_fn")]),
        # The ordinary lint reports the unwaived finding; the marker adds nothing.
        ("expand with an unwaived lock-timeout", marked("ALTER TABLE apps ADD COLUMN note text;"), []),
    ]

    def test_cases(self):
        for name, text, want in self.CASES:
            with self.subTest(name):
                self.assertEqual(marker_findings(text), want)

    def test_marker_cannot_be_waived(self):
        text = "-- migration-lint: allow compat-marker *: trust me\n-- +goose Up\nSELECT 1;\n"
        self.assertEqual(marker_findings(text), [("compat-marker", "*")])


def contract(up, catalogue=CATALOGUE):
    return sorted(f.target for f in ec.contract_statements("migrations/1_x.sql", marked(up), catalogue))


class ContractStatementTest(unittest.TestCase):
    CASES = [
        # name, Up section, contract-statement targets
        ("truncate", "TRUNCATE events;", ["events"]),
        ("drop function", "DROP FUNCTION IF EXISTS a(int), public.b(text, int) CASCADE;", ["a", "b"]),
        ("function dropped and created again", "DROP FUNCTION f(int);\nCREATE FUNCTION f(int, text) RETURNS int AS $$ SELECT 1 $$ LANGUAGE sql;", []),
        ("drop type", "DROP TYPE IF EXISTS app_tier;", ["app_tier"]),
        ("drop sequence", "DROP SEQUENCE apps_seq;", ["apps_seq"]),
        ("trigger dropped for good", "DROP TRIGGER IF EXISTS apps_notify ON apps;", ["apps.apps_notify"]),
        ("trigger replaced", "DROP TRIGGER IF EXISTS apps_notify ON apps;\n"
                             "CREATE TRIGGER apps_notify AFTER INSERT ON public.apps FOR EACH ROW EXECUTE FUNCTION f();", []),
        ("trigger on a table the migration creates", "DROP TRIGGER IF EXISTS t ON widgets;", []),
        ("drop unique index", "DROP INDEX IF EXISTS apps_slug_key;", ["apps_slug_key"]),
        ("drop plain index", "DROP INDEX IF EXISTS events_at_idx;", []),
        ("unique index rebuilt", "DROP INDEX apps_slug_key;\nCREATE UNIQUE INDEX apps_slug_key ON apps (slug, account_id);", []),
        ("drop unique constraint", "ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_pkey;", ["apps.apps_pkey"]),
        ("unique constraint re-added", "ALTER TABLE apps DROP CONSTRAINT apps_pkey, ADD CONSTRAINT apps_pkey PRIMARY KEY (id);", []),
        ("widened check", "ALTER TABLE apps DROP CONSTRAINT apps_tier_chk;\n"
                          "ALTER TABLE apps ADD CONSTRAINT apps_tier_chk CHECK (tier IN ('free', 'pro'));", []),
        ("dropped check loosens", "ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_tier_chk;", []),
        ("rename constraint", "ALTER TABLE apps RENAME CONSTRAINT a TO b;", ["apps"]),
        ("drop default", "ALTER TABLE apps ALTER COLUMN tier DROP DEFAULT;", ["apps"]),
        ("rename a function", "ALTER FUNCTION f(int) RENAME TO g;", ["f"]),
        ("function renamed aside and created again", "ALTER FUNCTION f(int) RENAME TO f_before;\n"
                                                     "CREATE FUNCTION f(int) RETURNS int AS $$ SELECT f_before($1) $$ LANGUAGE sql;", []),
        ("rename an enum value", "ALTER TYPE app_tier RENAME VALUE 'hobby' TO 'starter';", ["app_tier"]),
        ("add an enum value", "ALTER TYPE app_tier ADD VALUE IF NOT EXISTS 'team';", []),
        ("inside a DO block", "DO $$ BEGIN IF EXISTS (SELECT 1) THEN DROP FUNCTION f(); END IF; END $$;", ["f"]),
        ("additive statements", "CREATE TABLE widgets (id int PRIMARY KEY);\nCREATE OR REPLACE FUNCTION f() RETURNS int "
                                "AS $$ SELECT 1 $$ LANGUAGE sql;\nUPDATE apps SET tier = 'free' WHERE tier IS NULL;", []),
    ]

    def test_cases(self):
        for name, up, want in self.CASES:
            with self.subTest(name):
                self.assertEqual(contract(up), want)


class LaneVerdictTest(unittest.TestCase):
    """Whether a per-unit release may apply a migration ahead of restarts."""

    CASES = [
        # name, migration text, eligible, blocker substrings
        ("expand, additive", marked(LT + "ALTER TABLE apps ADD COLUMN note text;"), True, []),
        ("new table needs no lock_timeout", marked("CREATE TABLE widgets (id int);\nCREATE INDEX w ON widgets (id);"), True, []),
        ("contract class", marked("SELECT 1;", "contract"), False, ["declares migration-compat: contract"]),
        ("no class", "-- +goose Up\nSELECT 1;\n", False, ["declares no"]),
        ("unwaived finding", marked("ALTER TABLE apps ADD COLUMN note text;"), False, ["lint finding lock-timeout apps"]),
        ("drop of an unreferenced column", marked(LT + "ALTER TABLE apps DROP COLUMN legacy;"), False,
         ["contract change drop-column apps.legacy"]),
        ("waived drop of a column", marked("-- migration-lint: allow drop-column apps: not read since rc.250\n" + LT +
                                           "ALTER TABLE apps DROP COLUMN legacy;"), False,
         ["contract change drop-column apps.legacy"]),
        ("waived lock-timeout", marked("-- migration-lint: allow lock-timeout apps: tiny\nALTER TABLE apps ADD COLUMN n int;"),
         False, ["waives lock-timeout apps"]),
        ("waived concurrently in a transaction",
         marked("-- migration-lint: allow concurrently-in-transaction *: goose handles it\n"
                "CREATE INDEX CONCURRENTLY IF NOT EXISTS events_kind ON events (kind);"),
         False, ["waives concurrently-in-transaction"]),
        ("concurrent index", marked("CREATE INDEX CONCURRENTLY IF NOT EXISTS events_kind ON events (kind);",
                                    header="-- +goose NO TRANSACTION\n"), True, []),
        ("contract statement", marked(LT + "TRUNCATE events;"), False, ["contract change contract-statement events"]),
        ("a waived no-downtime rule still blocks",
         marked("-- migration-lint: allow unbatched-write apps: small table\nUPDATE apps SET tier = 'free';"), False,
         ["lint finding unbatched-write apps"]),
    ]

    def test_cases(self):
        for name, text, eligible, blockers in self.CASES:
            with self.subTest(name):
                got = ec.lane_verdict("migrations/20261018000000001_x.sql", text, CATALOGUE)
                self.assertEqual(got["eligible"], eligible, got["blockers"])
                self.assertEqual(len(got["blockers"]), len(blockers), got["blockers"])
                for have, want in zip(got["blockers"], blockers):
                    self.assertIn(want, have)

    def test_measured_waivers_stay_eligible_and_are_recorded(self):
        text = marked("-- migration-lint: allow index-build events: ~1 s write pause on 394k rows\n" + LT +
                      "CREATE INDEX events_kind ON events (kind);\n"
                      "-- migration-lint: allow table-rewrite apps.seen_at: 2k rows, measured 40 ms\n"
                      "ALTER TABLE apps ADD COLUMN seen_at timestamptz DEFAULT clock_timestamp();")
        got = ec.lane_verdict("migrations/20261018000000001_x.sql", text, CATALOGUE)
        self.assertTrue(got["eligible"], got["blockers"])
        self.assertEqual(got["waived"], ["index-build events", "table-rewrite apps.seen_at"])
        self.assertEqual(got["version"], 20261018000000001)
        self.assertEqual(got["compat"], "expand")
        self.assertEqual(len(got["sha256"]), 64)


class RangeTest(unittest.TestCase):
    """classify_range over a full release tag and the migrations since."""

    def git(self, *args):
        env = dict(os.environ, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@example.com",
                   GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@example.com")
        return subprocess.run(["git", "-C", self.repo, *args], check=True, capture_output=True, env=env, text=True).stdout

    def write(self, path, text):
        full = os.path.join(self.repo, path)
        os.makedirs(os.path.dirname(full), exist_ok=True)
        with open(full, "w", encoding="utf-8") as fh:
            fh.write(text)

    def commit(self, **files):
        for path, text in files.items():
            self.write(path.replace("__", "/"), text)
        self.git("add", "-A")
        self.git("commit", "-q", "-m", "change")
        return self.git("rev-parse", "HEAD").strip()

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.repo = self.tmp.name
        self.git("init", "-q", "-b", "main")
        self.commit(**{"schema.sql": "CREATE TABLE public.apps (\n);\n",
                       "migrations__00001_init.sql": marked("CREATE TABLE apps (id int);", "contract"),
                       "migrations__embed.go": "package migrations\n"})
        self.git("tag", "v0.1.0")

    def tearDown(self):
        self.tmp.cleanup()

    def classify(self):
        return ec.classify_range(self.repo, "v0.1.0", "HEAD")

    def test_no_migrations(self):
        self.commit(**{"pkg__x.go": "package pkg\n"})
        self.assertEqual(self.classify(), {"eligible": True, "migrations": [], "blockers": []})

    def test_expand_migrations_are_eligible_in_version_order(self):
        self.commit(**{"migrations__20261018000000002_b.sql": marked(LT + "ALTER TABLE apps ADD COLUMN b int;")})
        self.commit(**{"migrations__20261018000000001_a.sql": marked("CREATE TABLE widgets (id int);"),
                       "migrations__README.md": "docs\n", "migrations__20261018000000001_a_test.go": "package x\n"})
        got = self.classify()
        self.assertTrue(got["eligible"], got["blockers"])
        self.assertEqual([m["version"] for m in got["migrations"]], [20261018000000001, 20261018000000002])

    def test_one_contract_migration_closes_the_range(self):
        self.commit(**{"migrations__20261018000000001_a.sql": marked(LT + "ALTER TABLE apps ADD COLUMN b int;")})
        self.commit(**{"migrations__20261018000000002_drop.sql": marked(LT + "ALTER TABLE apps DROP COLUMN b;", "contract")})
        got = self.classify()
        self.assertFalse(got["eligible"])
        self.assertEqual([m["eligible"] for m in got["migrations"]], [True, False])
        self.assertEqual(got["blockers"], [
            "migrations/20261018000000002_drop.sql: declares migration-compat: contract",
            "migrations/20261018000000002_drop.sql: contract change drop-column apps.b at line 4",
        ])

    def test_objects_an_earlier_migration_in_the_range_created_are_existing(self):
        # An earlier unit release may have applied the first and run code
        # that reads widgets: dropping it is a contract change, and altering
        # it needs a lock_timeout.
        self.commit(**{"migrations__20261018000000001_a.sql": marked("CREATE TABLE widgets (id int);")})
        self.commit(**{"migrations__20261018000000002_b.sql": marked("DROP TABLE widgets;\nALTER TABLE apps ADD COLUMN c int;")})
        got = self.classify()
        self.assertFalse(got["eligible"])
        self.assertEqual(got["migrations"][1]["blockers"], [
            "lint finding lock-timeout widgets at line 3",
            "contract change drop-table widgets at line 3",
            "lint finding lock-timeout apps at line 4",
        ])

    def test_an_edited_issued_migration_closes_the_range(self):
        self.commit(**{"migrations__00001_init.sql": marked("CREATE TABLE apps (id bigint);", "contract")})
        got = self.classify()
        self.assertEqual(got["blockers"], ["migrations/00001_init.sql: an issued migration was modified or deleted"])


if __name__ == "__main__":
    unittest.main()
