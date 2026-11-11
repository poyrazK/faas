#!/usr/bin/env python3
"""Tests for the control-plane PostgreSQL activity collector."""

import importlib.machinery
import importlib.util
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "deploy/ansible/roles/postgres_metrics/files/faas-postgres-activity-metrics"
LOADER = importlib.machinery.SourceFileLoader("faas_postgres_activity_metrics", str(SCRIPT))
SPEC = importlib.util.spec_from_loader(LOADER.name, LOADER)
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
LOADER.exec_module(MODULE)


def fake_psql(overrides=None, fail=(), calls=None):
    """Answers each collector query by a fragment of its SQL. calls, when
    given, records each statements read ("counters" or "text")."""
    calls = [] if calls is None else calls
    table = {
        "FROM pg_stat_database": [["100", "2", "50", "950", "1", "2", "3", "4", "5", "0", "4096", "1048576"]],
        "GROUP BY 1, 2, 3, 4": [
            ["faas-schedd", "10.0.0.3", "active", "Lock", "3"],
            ["faas-schedd", "10.0.0.3", "idle", "Client", "12"],
            ["faas-apid", "local", "active", "none", "2"],
            ["weird name!", "local", "idle", "Client", "1"],
        ],
        "FILTER (WHERE state = 'active')": [["720.5", "12.25", "0"]],
        "FROM pg_stat_bgwriter": [["10", "2", "1500", "250", "300", "40", "7"]],
        "FROM pg_stat_user_tables": [
            ["instances", "500", "28586", "100", "2858600", "9000"],
            ["apps", "3", "193", "5", "965", "120"],
        ],
        "FROM pg_stat_statements\n": [
            ["123", "1000", "2500.0", "1000", "40", "SELECT * FROM instances WHERE node_id = $1"],
            ["-77", "5", "100.0", "5", "0", "ALTER ROLE faas PASSWORD $1"],
            ["456", "10", "50.0", "0", "0", "UPDATE apps SET password_hash = $1 WHERE id = $2"],
            ["789", "3", "10.0", "3", "0", "CREATE INDEX CONCURRENTLY x ON y (z)"],
        ],
        "pg_stat_statements_info": [["1792700000"]],
    }
    table.update(overrides or {})

    statements = table.pop("FROM pg_stat_statements\n")

    def q(db, sql):
        # Counters (no text) and the texts of named queryids both come from
        # the statement rows: queryid, calls, time, rows, blocks, text.
        if "pg_stat_statements(false)" in sql or "WHERE queryid IN" in sql:
            if "FROM pg_stat_statements\n" in fail:
                raise subprocess.CalledProcessError(1, ["psql"])
            calls.append("text" if "WHERE queryid IN" in sql else "counters")
            if "WHERE queryid IN" in sql:
                wanted = sql.split("WHERE queryid IN (")[1].split(")")[0].split(",")
                return [[r[0], r[5]] for r in statements if r[0] in wanted]
            return [r[:5] for r in statements]
        for fragment, rows in table.items():
            if fragment in sql:
                if fragment in fail:
                    raise subprocess.CalledProcessError(1, ["psql"])
                return rows
        raise AssertionError(f"unexpected query on {db}: {sql}")

    return q


class PostgresActivityMetricsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.out = Path(self.temp.name) / "faas_postgres_activity.prom"
        self.cache = Path(self.temp.name) / "statement-labels.json"
        self.env = {"FAAS_PG_METRICS_TEXTFILE": str(self.out), "FAAS_PG_METRICS_LABEL_CACHE": str(self.cache)}

    def tearDown(self):
        self.temp.cleanup()

    def run_collector(self, q):
        code = MODULE.main(q=q, env=self.env, now=lambda: 1792760000)
        return code, self.out.read_text()

    def test_every_section_renders(self):
        code, body = self.run_collector(fake_psql())
        self.assertEqual(code, 0)
        for line in (
            "faas_postgres_xact_commit_total 100",
            "faas_postgres_blocks_hit_total 950",
            'faas_postgres_tuples_total{op="deleted"} 5',
            "faas_postgres_database_size_bytes 1048576",
            'faas_postgres_backends{application="faas-schedd",client="10.0.0.3",state="active"} 3',
            'faas_postgres_backends{application="other",client="local",state="idle"} 1',
            'faas_postgres_backends_waiting{wait_event_type="Lock"} 3',
            "faas_postgres_oldest_transaction_seconds 720.500000",
            "faas_postgres_longest_query_seconds 12.250000",
            'faas_postgres_checkpoints_total{kind="requested"} 2',
            'faas_postgres_checkpoint_seconds_total{phase="write"} 1.500000',
            'faas_postgres_buffers_written_total{by="backend"} 7',
            'faas_postgres_table_seq_tuples_read_total{table="instances"} 2858600',
            'faas_postgres_statement_exec_seconds_total{queryid="123"} 2.500000',
            'faas_postgres_statement_info{queryid="123",query="SELECT * FROM instances WHERE node_id = $1"} 1',
            "faas_postgres_statement_stats_reset_timestamp_seconds 1792700000",
            'faas_postgres_activity_metrics_errors{section="statements"} 0',
            "faas_postgres_activity_metrics_last_run_timestamp_seconds 1792760000",
        ):
            self.assertIn(line, body)
        # Idle backends never count as waiting.
        self.assertNotIn('wait_event_type="Client"', body)

    def test_watched_tables_are_exported_whatever_their_rank(self):
        seen = []
        base = fake_psql({"FROM pg_stat_user_tables": [
            ["instances", "500", "28586", "100", "2858600", "9000"],
            ["managed_realtime_channel_heads", "0", "1234", "0", "0", "88"],
        ]})

        def q(db, sql):
            seen.append(sql)
            return base(db, sql)

        code, body = self.run_collector(q)
        self.assertEqual(code, 0)
        self.assertTrue(any("relname IN ('managed_realtime_channel_heads')" in sql for sql in seen))
        self.assertIn('faas_postgres_table_live_tuples{table="managed_realtime_channel_heads"} 1234', body)

    def test_only_plain_dml_text_is_exported(self):
        _, body = self.run_collector(fake_psql())
        self.assertNotIn("ALTER ROLE", body)
        self.assertNotIn("password", body.lower())
        self.assertNotIn("CREATE INDEX", body)
        self.assertNotIn('queryid="-77"', body)

    def test_statement_and_application_labels_are_bounded(self):
        many = [[f"app-{i}", "local", "idle", "Client", "1"] for i in range(40)]
        stmts = [[str(i), "1", str(1000 - i), "1", "0", f"SELECT {i} FROM t"] for i in range(50)]
        _, body = self.run_collector(fake_psql({"GROUP BY 1, 2, 3, 4": many, "FROM pg_stat_statements\n": stmts}))
        apps = {line.split('application="')[1].split('"')[0] for line in body.splitlines() if line.startswith("faas_postgres_backends{")}
        self.assertEqual(len(apps), MODULE.MAX_APPLICATIONS + 1)
        self.assertIn("other", apps)
        self.assertEqual(body.count("faas_postgres_statement_info{"), MODULE.TOP_STATEMENTS)

    def test_a_failing_section_is_reported_and_the_rest_still_written(self):
        code, body = self.run_collector(fake_psql(fail=("FROM pg_stat_statements\n",)))
        self.assertEqual(code, 1)
        self.assertIn('faas_postgres_activity_metrics_errors{section="statements"} 1', body)
        self.assertIn('faas_postgres_activity_metrics_errors{section="database"} 0', body)
        self.assertIn("faas_postgres_xact_commit_total 100", body)
        self.assertNotIn("faas_postgres_statement_info", body)

    def test_statement_text_is_read_only_for_new_statements(self):
        # The text file is ~400 ms to read on production; the counters ~85 ms.
        calls = []
        q = fake_psql(calls=calls)
        _, first = self.run_collector(q)
        self.assertEqual(calls, ["counters", "text"])
        calls.clear()
        _, second = self.run_collector(q)
        self.assertEqual(calls, ["counters"], "a known statement set must not read the text again")
        self.assertIn('query="SELECT * FROM instances WHERE node_id = $1"', second)
        self.assertNotIn("ALTER ROLE", second)
        self.assertNotIn('queryid="-77"', second)

    def test_a_new_statement_waits_for_the_refresh_interval(self):
        calls = []
        stmts = [["123", "1000", "2500.0", "1000", "40", "SELECT 1 FROM t"]]
        self.run_collector(fake_psql({"FROM pg_stat_statements\n": stmts}, calls=calls))
        stmts.append(["999", "50", "9000.0", "5", "0", "SELECT 2 FROM u"])
        calls.clear()
        _, body = self.run_collector(fake_psql({"FROM pg_stat_statements\n": stmts}, calls=calls))
        self.assertEqual(calls, ["counters"])
        self.assertNotIn('queryid="999"', body, "unlabelled until the next text read")
        calls.clear()
        MODULE.main(q=fake_psql({"FROM pg_stat_statements\n": stmts}, calls=calls), env=self.env,
                    now=lambda: 1792760000 + MODULE.LABEL_REFRESH_SECONDS)
        self.assertEqual(calls, ["counters", "text"])
        self.assertIn('query="SELECT 2 FROM u"', self.out.read_text())

    def test_an_unusable_cache_reads_the_text_every_run(self):
        self.env["FAAS_PG_METRICS_LABEL_CACHE"] = str(Path(self.temp.name) / "missing-dir" / "labels.json")
        calls = []
        q = fake_psql(calls=calls)
        self.run_collector(q)
        self.run_collector(q)
        self.assertEqual(calls, ["counters", "text", "counters", "text"])

    def test_database_names_must_be_identifiers(self):
        with self.assertRaises(ValueError):
            MODULE.collect(fake_psql(), {"FAAS_PG_METRICS_DB": "faas'; DROP"})

    @unittest.skipUnless(shutil.which("promtool"), "promtool not installed")
    def test_output_passes_promtool(self):
        _, body = self.run_collector(fake_psql())
        res = subprocess.run(["promtool", "check", "metrics"], input=body, capture_output=True, text=True)
        self.assertEqual(res.stdout + res.stderr, "", res.stdout + res.stderr)


if __name__ == "__main__":
    unittest.main()
