#!/usr/bin/env python3
"""The control plane's record of unit releases not yet watched clean
(docs/adr/20261020214632-automatic-unit-deploy-safety-rails.md, section 3).

The post-deploy watch keeps the SLO gate running for 30 minutes after a run's
last activation. A newer run may deploy meanwhile and take the watch over, so
what one rollback must undo can span runs. This record says, per host:

  known_good  the newest release on the host that finished a clean watch (or
              the one active before the first unwatched deploy): a breach
              rolls the host back to it
  current     the release the last unit deploy activated there; a host whose
              active release differs was changed by something else, and the
              watch leaves it alone
  releases    the unit releases activated since known_good, oldest first; at
              most `max_unwatched` (scripts/ci/release_units.json "watch"), so
              release retention still holds known_good and one rollback
              undoes a bounded number of merges
  base        the host's base full release; a full release since then voids
              the entry
  services, verify, gate, commits
              what changed since known_good: the services to roll back, their
              readiness checks, the units the watch judges, and the merge
              commits whose post-merge light runs it reads
  target      how the watch reaches a compute node (null for the control plane)

and for the record: the run that owns the watch (the newest that deployed),
and when each host was first and last activated.

The file lives on the control plane (default /var/lib/faas/deploy/
unit-watch.json, root, 0600) and is changed under an flock on its .lock file.
unit_deploy.sh copies this script there and runs it as root:

  unit_watch_record.py open   --record P --owner RUN --host H --plan PLAN --live ID --base SHA --now T
                              [--target-file JSON_FILE] [--commit SHA] [--max-unwatched N]
  unit_watch_record.py revert --record P --host H --live ID
  unit_watch_record.py show   --record P
  unit_watch_record.py close  --record P --owner RUN
  unit_watch_record.py adopt  --record P --owner RUN --now T

adopt hands a record no watch runs for (the last one ended inconclusive or
was cancelled) to a new watch, which watches a full window from now.

open exits 3 when the host already holds max_unwatched releases; close
exits 4 when another run owns the record. Every command prints the record
(or null) as JSON.
"""
from __future__ import annotations

import argparse
import contextlib
import fcntl
import json
import os
import re
import sys
import tempfile

KIND = "gregale-unit-watch"
SHA = re.compile(r"^[0-9a-f]{40}$")
HOST = re.compile(r"^[a-z0-9][a-z0-9.-]*$")
RUN = re.compile(r"^[0-9]+$")
CAP_REACHED, NOT_OWNER = 3, 4


class RecordError(Exception):
    pass


@contextlib.contextmanager
def locked(path: str):
    """Hold an exclusive flock on PATH.lock while the record changes."""
    os.makedirs(os.path.dirname(path) or ".", mode=0o700, exist_ok=True)
    fd = os.open(path + ".lock", os.O_CREAT | os.O_RDWR, 0o600)
    try:
        fcntl.flock(fd, fcntl.LOCK_EX)
        yield
    finally:
        os.close(fd)


def load(path: str) -> dict | None:
    try:
        with open(path, encoding="utf-8") as f:
            record = json.load(f)
    except FileNotFoundError:
        return None
    if not isinstance(record, dict) or record.get("kind") != KIND:
        raise RecordError(f"{path} is not a unit watch record")
    return record


def save(path: str, record: dict | None) -> None:
    if record is None or not record.get("hosts"):
        with contextlib.suppress(FileNotFoundError):
            os.remove(path)
        return
    fd, tmp = tempfile.mkstemp(dir=os.path.dirname(path) or ".", prefix=".unit-watch.")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            json.dump(record, f, indent=1, sort_keys=True)
            f.write("\n")
        os.chmod(tmp, 0o600)
        os.replace(tmp, path)
    except BaseException:
        with contextlib.suppress(FileNotFoundError):
            os.remove(tmp)
        raise


def union(old: list, new: list) -> list:
    out = list(old)
    for item in new:
        if item not in out:
            out.append(item)
    return out


def open_host(record: dict | None, *, owner: str, host: str, plan: dict, live: str, base: str, now: int,
              target: dict | None = None, commit: str | None = None, max_unwatched: int = 2) -> dict:
    """Add the plan's activation of host to the record and make owner its
    owner. Raise RecordError (cap) when host already holds max_unwatched
    releases and the plan adds another."""
    release, expect = plan["release_id"], plan["expect_current"]
    for name, value in (("release_id", release), ("expect_current", expect), ("live", live), ("base", base)):
        if not SHA.match(value or ""):
            raise RecordError(f"{name} must be 40 hex: {value!r}")
    if not HOST.match(host) or not RUN.match(owner):
        raise RecordError(f"bad host {host!r} or owner {owner!r}")
    if record is None:
        record = {"kind": KIND, "opened_at": now, "hosts": {}, "order": []}
    entry = record["hosts"].get(host)
    # The host moved under the record (a full release, a manual deploy or
    # rollback): what the entry knew no longer describes it.
    if entry is not None and (entry["base"] != base or entry["current"] != live):
        record["hosts"].pop(host)
        record["order"] = [h for h in record["order"] if h != host]
        entry = None
    if entry is None:
        entry = {"known_good": expect, "current": live, "base": base, "releases": [], "services": [],
                 "verify": [], "gate": [], "commits": [], "target": target,
                 "first_activation": now, "last_activation": now}
    adds = release != entry["known_good"] and release not in entry["releases"]
    if adds and len(entry["releases"]) >= max_unwatched:
        raise RecordError(f"{host} already holds {len(entry['releases'])} unwatched releases "
                          f"(max {max_unwatched}); wait for the watch to finish")
    if adds:
        entry["releases"].append(release)
    entry["current"] = release
    entry["services"] = union(entry["services"], plan.get("only") or [])
    entry["verify"] = union(entry["verify"], plan.get("verify") or [])
    entry["gate"] = union(entry["gate"], plan.get("gate") or [])
    if commit:
        if not SHA.match(commit):
            raise RecordError(f"commit must be 40 hex: {commit!r}")
        entry["commits"] = union(entry["commits"], [commit])
    if target is not None:
        entry["target"] = target
    entry["last_activation"] = now
    if not entry["releases"]:
        # A converge rerun of a release already watched clean: nothing new.
        record["hosts"].pop(host, None)
        record["order"] = [h for h in record["order"] if h != host]
        if not record["hosts"]:
            return None
    else:
        record["hosts"][host] = entry
        if host not in record["order"]:
            record["order"].append(host)
    record["owner_run"] = owner
    record["updated_at"] = now
    return record


def revert_host(record: dict | None, *, host: str, live: str) -> dict | None:
    """A deploy that failed put host back on live: forget what it added."""
    if record is None or host not in record["hosts"]:
        return record
    entry = record["hosts"][host]
    if live == entry["known_good"]:
        record["hosts"].pop(host)
        record["order"] = [h for h in record["order"] if h != host]
    elif live in entry["releases"]:
        entry["releases"] = entry["releases"][: entry["releases"].index(live) + 1]
        entry["current"] = live
    else:
        # The host is on a release the record never saw: leave the entry
        # stale (current differs), so the watch leaves the host alone.
        pass
    return record if record["hosts"] else None


def adopt(record: dict | None, *, owner: str, now: int) -> dict | None:
    """Make owner the watch of an orphaned record, watching from now."""
    if not RUN.match(owner):
        raise RecordError(f"bad owner {owner!r}")
    if record is None:
        return None
    record["owner_run"] = owner
    record["watch_from"] = now
    record["updated_at"] = now
    return record


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    sub = parser.add_subparsers(dest="command", required=True)
    for name in ("open", "revert", "show", "close", "adopt"):
        p = sub.add_parser(name)
        p.add_argument("--record", required=True)
        if name in ("open", "close", "adopt"):
            p.add_argument("--owner", required=True)
        if name == "adopt":
            p.add_argument("--now", type=int, required=True)
        if name in ("open", "revert"):
            p.add_argument("--host", required=True)
            p.add_argument("--live", required=True)
        if name == "open":
            p.add_argument("--plan", required=True)
            p.add_argument("--base", required=True)
            p.add_argument("--now", type=int, required=True)
            p.add_argument("--target-file", default="")
            p.add_argument("--commit", default="")
            p.add_argument("--max-unwatched", type=int, default=2)
    args = parser.parse_args(argv)
    try:
        with locked(args.record):
            record = load(args.record)
            code = 0
            if args.command == "open":
                with open(args.plan, encoding="utf-8") as f:
                    plan = json.load(f)
                target = None
                if args.target_file:
                    with open(args.target_file, encoding="utf-8") as f:
                        target = json.load(f)
                try:
                    record = open_host(record, owner=args.owner, host=args.host, plan=plan, live=args.live,
                                       base=args.base, now=args.now, target=target, commit=args.commit or None,
                                       max_unwatched=args.max_unwatched)
                except RecordError as err:
                    if "unwatched releases" not in str(err):
                        raise
                    print(f"unit-watch: {err}", file=sys.stderr)
                    code = CAP_REACHED
                else:
                    save(args.record, record)
            elif args.command == "revert":
                record = revert_host(record, host=args.host, live=args.live)
                save(args.record, record)
            elif args.command == "adopt":
                record = adopt(record, owner=args.owner, now=args.now)
                save(args.record, record)
            elif args.command == "close":
                if record is not None and record.get("owner_run") != args.owner:
                    print(f"unit-watch: run {record.get('owner_run')} owns the record, not {args.owner}", file=sys.stderr)
                    code = NOT_OWNER
                else:
                    record = None
                    save(args.record, None)
    except (RecordError, OSError, ValueError, KeyError) as err:
        print(f"unit-watch: {err}", file=sys.stderr)
        return 2
    print(json.dumps(record, sort_keys=True))
    return code


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
