#!/usr/bin/env python3
"""Bring a migration-carrying branch level with origin/main (`make migrations-land`).

Every PR that adds a migration hits the same chore whenever main moves:

1. `git merge origin/main` conflicts in schema.sql.migrations.sha256 (a digest
   of every migration file), and sometimes main has landed a migration whose
   version is newer than the branch's, so the branch's would apply out of order.
2. schema.sql, its digest and the sqlc code must be regenerated from a real
   Postgres.

This script does both. It merges origin/main (unless a merge is already in
progress), takes main's side of the digest, renames the branch's new
migrations to fresh versions after main's newest (keeping their order and
rewriting references to the old versions inside them), regenerates schema.sql
against DATABASE_URL or a throwaway Postgres it starts itself, runs
sqlc-generate and migration-lint, and stages the result. It never commits or
pushes. Any conflict outside the digest stops it for a human.
"""

from __future__ import annotations

import argparse
import os
import random
import re
import shutil
import subprocess
import sys
import tempfile
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Callable

DIGEST = "schema.sql.migrations.sha256"
VERSION_RE = re.compile(r"^(\d{17})_(.+\.sql)$")


def run(*args: str, check: bool = True, capture: bool = False, env: dict[str, str] | None = None) -> str:
    proc = subprocess.run(args, check=False, text=True, capture_output=capture, env=env)
    if check and proc.returncode != 0:
        detail = (proc.stderr or proc.stdout or "").strip() if capture else ""
        raise SystemExit(f"migrations-land: {' '.join(args)} failed ({proc.returncode}) {detail}")
    return proc.stdout if capture else ""


def git(*args: str, check: bool = True) -> str:
    return run("git", *args, check=check, capture=True)


def merge_in_progress() -> bool:
    return git("rev-parse", "-q", "--verify", "MERGE_HEAD", check=False).strip() != ""


def versions(names: list[str]) -> list[tuple[str, str]]:
    out = []
    for name in names:
        m = VERSION_RE.match(Path(name).name)
        if m:
            out.append((m.group(1), m.group(2)))
    return out


def plan_renames(branch: list[str], main: list[str], now: datetime,
                 jitter: Callable[[], int] = lambda: random.randint(1, 3600)) -> dict[str, str]:
    """Return old filename -> new filename for branch migrations that main's
    newest migration now precedes or ties. Order among the branch's own
    migrations is kept; new versions share a fresh timestamp and count up
    from 100 in steps of 100 so later hand-inserts still fit.

    The stamp is the current UTC time. When main already holds a version
    stamped later than the clock, the stamp is that version plus a random
    1-3600 s, not plus 1 s: every branch landed in that window would
    otherwise get the same version and collide once both merge."""
    mine = sorted(versions(branch))
    theirs = versions(main)
    if not mine or not theirs:
        return {}
    newest_main = max(v for v, _ in theirs)
    if min(v for v, _ in mine) > newest_main:
        return {}
    stamp = now.strftime("%Y%m%d%H%M%S")
    if stamp + "000" <= newest_main:
        ahead = datetime.strptime(newest_main[:14], "%Y%m%d%H%M%S") + timedelta(seconds=jitter())
        stamp = ahead.strftime("%Y%m%d%H%M%S")
    renames = {}
    for i, (version, rest) in enumerate(mine, start=1):
        renames[f"{version}_{rest}"] = f"{stamp}{i * 100:03d}_{rest}"
    return renames


def branch_migrations(base: str) -> list[str]:
    out = git("diff", "--name-only", "--diff-filter=A", f"{base}...HEAD", "--", "migrations/")
    # base...HEAD diffs from the merge base, so this is the branch's own new
    # files both before and after main is merged in.
    return [Path(p).name for p in out.split() if p.endswith(".sql")]


def main_migrations(ref: str) -> list[str]:
    out = git("ls-tree", "--name-only", f"{ref}:migrations")
    return [n for n in out.split() if n.endswith(".sql")]


def apply_renames(renames: dict[str, str], base: str) -> list[str]:
    """Rename the files and rewrite the old versions everywhere the branch
    refers to them: its migrations and any other file it changed (an ADR or
    test naming a migration)."""
    old_versions = {old[:17]: new[:17] for old, new in renames.items()}
    for old, new in renames.items():
        git("mv", f"migrations/{old}", f"migrations/{new}")
    changed = git("diff", "--name-only", "--diff-filter=AM", f"{base}...HEAD").split()
    touched = []
    renamed = {f"migrations/{new}" for new in renames.values()}
    paths = renamed | {p for p in changed if not (p.startswith("migrations/") and p.endswith(".sql"))}
    for p in sorted(paths):
        path = Path(p)
        try:
            text = path.read_text()
        except (FileNotFoundError, UnicodeDecodeError):
            continue
        updated = text
        for old_v, new_v in old_versions.items():
            updated = updated.replace(old_v, new_v)
        if updated != text:
            path.write_text(updated)
            if p not in renamed:
                touched.append(p)
                print(f"migrations-land: updated references in {p}")
    for old, new in renames.items():
        print(f"migrations-land: renamed {old} -> {new}")
    return touched


def postgres_bin_dir() -> str | None:
    """PG_BIN, else the PATH, else a keg-only Homebrew or Debian install
    (16 first: the version schema.sql is dumped with)."""
    if os.environ.get("PG_BIN"):
        return os.environ["PG_BIN"]
    if shutil.which("initdb") and shutil.which("pg_dump"):
        return None
    candidates = sorted(Path("/opt/homebrew/opt").glob("postgresql@*/bin")) + sorted(Path("/usr/lib/postgresql").glob("*/bin"))
    candidates.sort(key=lambda p: (not str(p).endswith(("@16/bin", "/16/bin")), str(p)))
    for c in candidates:
        if (c / "initdb").exists() and (c / "pg_dump").exists():
            return str(c)
    return None


class ThrowawayPostgres:
    """A private Postgres on a unix socket in a temp directory."""

    def __init__(self) -> None:
        self.dir = Path(tempfile.mkdtemp(prefix="migrations-land-"))
        self.data = self.dir / "data"
        self.sock = self.dir / "sock"

    def __enter__(self) -> str:
        for tool in ("initdb", "pg_ctl", "createdb"):
            if not shutil.which(tool):
                raise SystemExit(f"migrations-land: {tool} is not on PATH; set DATABASE_URL to a scratch database instead")
        self.sock.mkdir()
        run("initdb", "-D", str(self.data), "-U", "faas", "-A", "trust", capture=True)
        run("pg_ctl", "-D", str(self.data), "-o", f"-p 55499 -k {self.sock} -c listen_addresses=''",
            "-l", str(self.dir / "log"), "-w", "start", capture=True)
        run("createdb", "-h", str(self.sock), "-p", "55499", "-U", "faas", "schemadump", capture=True)
        return f"postgres:///schemadump?host={self.sock}&port=55499&user=faas"

    def __exit__(self, *_: object) -> None:
        run("pg_ctl", "-D", str(self.data), "-m", "fast", "stop", check=False, capture=True)
        shutil.rmtree(self.dir, ignore_errors=True)


def regenerate(database_url: str | None) -> None:
    def dump(url: str) -> None:
        env = dict(os.environ, DATABASE_URL=url)
        for attempt in range(3):
            proc = subprocess.run(["go", "run", "./cmd/schema-dump", "-o", "schema.sql"], env=env, text=True, capture_output=True)
            if proc.returncode == 0:
                print(proc.stdout.strip())
                return
            # A shared build cache trimmed mid-compile fails with missing
            # cache entries; a retry rebuilds them.
            print(f"migrations-land: schema-dump failed (attempt {attempt + 1}): {proc.stderr.strip().splitlines()[-1:]}")
            time.sleep(10)
        raise SystemExit("migrations-land: schema-dump kept failing")

    if database_url:
        dump(database_url)
    else:
        with ThrowawayPostgres() as url:
            dump(url)
    run("make", "sqlc-generate")
    run("make", "migration-lint")


def main() -> int:
    pg_bin = postgres_bin_dir()
    if pg_bin:
        os.environ["PATH"] = pg_bin + os.pathsep + os.environ["PATH"]
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--remote", default="origin")
    parser.add_argument("--branch", default="main")
    parser.add_argument("--no-merge", action="store_true", help="only rename and regenerate (no fetch or merge)")
    args = parser.parse_args()
    upstream = f"{args.remote}/{args.branch}"

    if not args.no_merge and not merge_in_progress():
        git("fetch", "-q", args.remote, args.branch)
        subprocess.run(["git", "merge", "--no-edit", upstream], text=True, capture_output=True)
    conflicts = [c for c in git("diff", "--name-only", "--diff-filter=U").split() if c]
    others = [c for c in conflicts if c != DIGEST]
    if others:
        print("migrations-land: resolve these conflicts by hand, then run it again:", *others, sep="\n  ")
        return 2
    if DIGEST in conflicts:
        git("checkout", "--theirs", DIGEST)

    renames = plan_renames(branch_migrations(upstream), main_migrations(upstream), datetime.now(timezone.utc))
    touched = apply_renames(renames, upstream) if renames else []

    regenerate(os.environ.get("DATABASE_URL") or None)
    git("add", "-A", "schema.sql", DIGEST, "pkg/state/sqlc", "migrations", *touched)
    state = "merge is staged; commit it" if merge_in_progress() else "changes are staged"
    print(f"migrations-land: done; {state}, then push")
    return 0


if __name__ == "__main__":
    sys.exit(main())
