#!/usr/bin/env python3
"""Unit tests for compute_db_roles.py (per-daemon compute database logins)."""
import base64
import hashlib
import hmac
import importlib.util
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest
import urllib.parse

HERE = pathlib.Path(__file__).parent
SCRIPT = HERE / "compute_db_roles.py"
spec = importlib.util.spec_from_file_location("compute_db_roles", SCRIPT)
cdr = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cdr)

KEY = "k" * 40
BASE = "postgres://faas_compute:old-secret@fsn-1.gregale.dev:5432/faas?sslmode=require"


def run(mode, path, **env):
    full = {k: v for k, v in os.environ.items()
            if k not in ("POSTGRES_COMPUTE_DAEMON_ROLES_ENABLED", "POSTGRES_COMPUTE_ROLE_KEY", "COMPUTE_DATABASE_URL")}
    full.update(env)
    return subprocess.run([sys.executable, "-I", str(SCRIPT), mode, path], env=full, capture_output=True, text=True)


def scram_matches(verifier, password):
    """Checks a password against a SCRAM-SHA-256 verifier the way PostgreSQL does."""
    head, rest = verifier.split("$", 1)
    iterations, salt = rest.split("$")[0].split(":")
    stored, server = rest.split("$")[1].split(":")
    salted = hashlib.pbkdf2_hmac("sha256", password.encode(), base64.b64decode(salt), int(iterations))
    client = hmac.new(salted, b"Client Key", hashlib.sha256).digest()
    return head == "SCRAM-SHA-256" and hashlib.sha256(client).digest() == base64.b64decode(stored) \
        and hmac.new(salted, b"Server Key", hashlib.sha256).digest() == base64.b64decode(server)


class ComputeDBRoles(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)

    def path(self, name):
        return os.path.join(self.dir.name, name)

    def test_disabled_writes_nothing(self):
        for value in ("", "false"):
            env = {"POSTGRES_COMPUTE_DAEMON_ROLES_ENABLED": value} if value else {}
            for mode in ("verifiers", "dsns"):
                out = self.path(f"{mode}-{value or 'unset'}")
                self.assertEqual(run(mode, out, **env).returncode, 0)
                self.assertFalse(os.path.exists(out))

    def test_rejects_bad_switch_and_short_key(self):
        self.assertNotEqual(run("dsns", self.path("a"), POSTGRES_COMPUTE_DAEMON_ROLES_ENABLED="yes").returncode, 0)
        r = run("dsns", self.path("b"), POSTGRES_COMPUTE_DAEMON_ROLES_ENABLED="true",
                POSTGRES_COMPUTE_ROLE_KEY="short", COMPUTE_DATABASE_URL=BASE)
        self.assertNotEqual(r.returncode, 0)
        self.assertFalse(os.path.exists(self.path("b")))

    def test_dsns_and_verifiers_agree(self):
        dsns, varsf = self.path("dsns"), self.path("vars.yml")
        with open(varsf, "w") as f:
            f.write("faas_domain: example.test\n")
        env = {"POSTGRES_COMPUTE_DAEMON_ROLES_ENABLED": "true", "POSTGRES_COMPUTE_ROLE_KEY": KEY,
               "COMPUTE_DATABASE_URL": BASE}
        self.assertEqual(run("dsns", dsns, **env).returncode, 0)
        self.assertEqual(run("verifiers", varsf, **env).returncode, 0)
        self.assertEqual(os.stat(dsns).st_mode & 0o777, 0o600)
        verifiers = {}
        body = open(varsf).read()
        self.assertIn("faas_domain: example.test\n", body)
        for line in body.splitlines():
            if line.startswith("  faas_"):
                role, value = line.strip().split(": ", 1)
                verifiers[role] = value.strip('"')
        self.assertEqual(set(verifiers), set(cdr.UNIT_ROLES.values()))
        units = {}
        for line in open(dsns).read().splitlines():
            unit, dsn = line.split("=", 1)
            units[unit] = urllib.parse.urlsplit(dsn)
        self.assertEqual(set(units), set(cdr.UNIT_ROLES))
        for unit, parts in units.items():
            role = cdr.UNIT_ROLES[unit]
            self.assertEqual(parts.username, role)
            self.assertEqual((parts.hostname, parts.port, parts.path, parts.query),
                             ("fsn-1.gregale.dev", 5432, "/faas", "sslmode=require"))
            self.assertTrue(scram_matches(verifiers[role], parts.password), unit)
            self.assertNotIn("old-secret", parts.geturl())
        # Distinct passwords, stable across runs.
        self.assertEqual(len({p.password for p in units.values()}), len(units))
        self.assertEqual(cdr.verifier(KEY.encode(), "faas_vmmd"), cdr.verifier(KEY.encode(), "faas_vmmd"))

    def test_refuses_owner_base_and_duplicate_vars(self):
        env = {"POSTGRES_COMPUTE_DAEMON_ROLES_ENABLED": "true", "POSTGRES_COMPUTE_ROLE_KEY": KEY}
        r = run("dsns", self.path("o"), COMPUTE_DATABASE_URL=BASE.replace("faas_compute:", "faas:"), **env)
        self.assertNotEqual(r.returncode, 0)
        varsf = self.path("dup.yml")
        with open(varsf, "w") as f:
            f.write("faas_postgres_compute_daemon_role_verifiers: {}\n")
        self.assertNotEqual(run("verifiers", varsf, **env).returncode, 0)


if __name__ == "__main__":
    unittest.main()
