#!/usr/bin/env python3
"""Per-daemon database logins for compute nodes (ADR-20261023104734 step 3).

Every compute daemon logs in as its own faas_runtime member instead of the
shared faas_compute. The passwords are never stored: each one is
HMAC-SHA256(POSTGRES_COMPUTE_ROLE_KEY, "gregale-db-role:" + role), so the
control plane (which creates the roles) and cd-compute (which writes each
node's DSNs) derive the same value from the one environment secret.

  compute_db_roles.py verifiers VARS_FILE
      Appends faas_postgres_compute_daemon_role_verifiers (role -> SCRAM-SHA-256
      verifier) to the control-plane converge vars. The salt is derived too, so
      the verifier is stable and the postgres role stays idempotent.
  compute_db_roles.py dsns OUT_FILE
      Writes "<unit>=<dsn>" lines for node_join from COMPUTE_DATABASE_URL, with
      each unit's role and password in place of faas_compute's.

Both do nothing unless POSTGRES_COMPUTE_DAEMON_ROLES_ENABLED is "true".
"""
import base64
import hashlib
import hmac
import os
import sys
import urllib.parse

# unit -> role. node_join, the postgres role and runtime-roles.sql read this
# list through pkg/daemonunitspec's tests; keep them in step.
UNIT_ROLES = {
    "faas-vmmd": "faas_vmmd",
    "faas-schedd": "faas_node_schedd",
    "faas-imaged": "faas_imaged",
    "faas-builderd": "faas_builderd",
    "faas-gatewayd-internal": "faas_gatewayd_internal",
    "faas-gatewayd-public-edge": "faas_gatewayd_edge",
}
SCRAM_ITERATIONS = 4096


def fail(msg):
    print(f"::error::compute_db_roles: {msg}", file=sys.stderr)
    sys.exit(1)


def enabled():
    value = os.environ.get("POSTGRES_COMPUTE_DAEMON_ROLES_ENABLED", "")
    if value in ("", "false"):
        return False
    if value != "true":
        fail(f'POSTGRES_COMPUTE_DAEMON_ROLES_ENABLED must be true or false, not "{value}"')
    return True


def role_key():
    key = os.environ.get("POSTGRES_COMPUTE_ROLE_KEY", "").strip()
    if len(key) < 32:
        fail("POSTGRES_COMPUTE_ROLE_KEY must be set to at least 32 characters (an environment secret)")
    return key.encode()


def derive(key, label):
    return hmac.new(key, label.encode(), hashlib.sha256).digest()


def password(key, role):
    return derive(key, "gregale-db-role:" + role).hex()


def verifier(key, role):
    pw = password(key, role).encode()
    salt = derive(key, "gregale-db-role-salt:" + role)[:16]
    salted = hashlib.pbkdf2_hmac("sha256", pw, salt, SCRAM_ITERATIONS)
    client = hmac.new(salted, b"Client Key", hashlib.sha256).digest()
    stored = hashlib.sha256(client).digest()
    server = hmac.new(salted, b"Server Key", hashlib.sha256).digest()
    b64 = lambda b: base64.b64encode(b).decode()
    return f"SCRAM-SHA-256${SCRAM_ITERATIONS}:{b64(salt)}${b64(stored)}:{b64(server)}"


def dsn_for(base, role, pw):
    parts = urllib.parse.urlsplit(base)
    if parts.scheme not in ("postgres", "postgresql") or not parts.hostname:
        fail("COMPUTE_DATABASE_URL must be a postgres:// URL with a host")
    if parts.username != "faas_compute":
        fail(f'COMPUTE_DATABASE_URL logs in as "{parts.username}"; move compute nodes to faas_compute first')
    host = parts.hostname if ":" not in parts.hostname else f"[{parts.hostname}]"
    netloc = f"{role}:{pw}@{host}" + (f":{parts.port}" if parts.port else "")
    return urllib.parse.urlunsplit((parts.scheme, netloc, parts.path, parts.query, ""))


def write_private(path, text, append=False):
    old = os.umask(0o077)
    try:
        with open(path, "a" if append else "w") as f:
            f.write(text)
    finally:
        os.umask(old)
    os.chmod(path, 0o600)


def cmd_verifiers(path):
    if os.path.exists(path):
        with open(path) as f:
            if any(line.startswith("faas_postgres_compute_daemon_role_verifiers:") for line in f):
                fail(f"{path} already sets faas_postgres_compute_daemon_role_verifiers; CD derives it")
    key = role_key()
    lines = ["", "# Compute daemon roles staged by CD (scripts/ci/compute_db_roles.py).",
             "faas_postgres_compute_daemon_role_verifiers:"]
    lines += [f'  {role}: "{verifier(key, role)}"' for role in UNIT_ROLES.values()]
    write_private(path, "\n".join(lines) + "\n", append=True)


def cmd_dsns(path):
    base = os.environ.get("COMPUTE_DATABASE_URL", "").strip()
    if not base:
        fail("COMPUTE_DATABASE_URL is not set")
    key = role_key()
    lines = [f"{unit}={dsn_for(base, role, password(key, role))}" for unit, role in UNIT_ROLES.items()]
    write_private(path, "\n".join(lines) + "\n")


def main(argv):
    if len(argv) != 3 or argv[1] not in ("verifiers", "dsns"):
        print("usage: compute_db_roles.py verifiers VARS_FILE | dsns OUT_FILE", file=sys.stderr)
        return 2
    if not enabled():
        return 0
    {"verifiers": cmd_verifiers, "dsns": cmd_dsns}[argv[1]](argv[2])
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
