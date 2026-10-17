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

CATALOGUE = ec.Catalogue(relations={"apps", "events", "app_summary"}, indexes={"events_at_idx"})


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
  ALTER TABLE apps ADD CONSTRAINT x CHECK(note <> '');
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
        self.assertEqual(lint(LT + "ALTER TABLE apps ADD COLUMN tier text NOT NULL DEFAULT 'free' CHECK (tier <> '');"), [])

    def test_check_is_not_null_is_not_a_not_null_column(self):
        self.assertEqual(lint(LT + "ALTER TABLE apps ADD COLUMN tier text CHECK (tier IS NOT NULL OR true);"), [])

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


class CatalogueTest(unittest.TestCase):
    def test_load_catalogue(self):
        cat = ec.load_catalogue("CREATE TABLE public.apps (\nCREATE VIEW public.app_summary AS\n"
                                "CREATE MATERIALIZED VIEW public.mv AS\nCREATE UNIQUE INDEX apps_slug ON public.apps USING btree (slug);\n")
        self.assertEqual(cat.relations, {"apps", "app_summary", "mv"})
        self.assertEqual(cat.indexes, {"apps_slug"})


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

    def run_main(self):
        self.write("migrations/20261017000000001_drop_legacy_tier.sql",
                   "-- +goose Up\nSET LOCAL lock_timeout = '5s';\nALTER TABLE apps DROP COLUMN legacy_tier;\n-- +goose Down\nSELECT 1;\n")
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


if __name__ == "__main__":
    unittest.main()
