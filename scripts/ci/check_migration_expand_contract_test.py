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

CATALOGUE = ec.Catalogue(relations={"apps", "events", "app_summary"}, indexes={"events_at_idx", "apps_slug_key"},
                         unique={"apps_slug_key", "apps_pkey"})


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
        self.assertEqual(cat.unique, {"apps_slug"})

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
        ("contract statement", marked("TRUNCATE events;"), False, ["contract change contract-statement events"]),
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
