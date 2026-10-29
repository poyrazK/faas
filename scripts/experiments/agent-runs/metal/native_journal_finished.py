#!/usr/bin/env python3
"""Report whether every record in a vmmd native-process journal is finished.

    native_journal_finished.py JOURNAL_DIR

A record is finished by its own kind's flag:
  tun-binds/, image-sources/, loop-mounts/   "removed": true
  qualifications/                            "revoked": true and its deadline passed; a
                                             record without a deadline (a capture,
                                             backing or load) follows the qualification
                                             of its name in the nearest directory above
  helpers/                                   "launch": {"resources_removed": true}
  any other record                           "resources_removed": true
A file that does not parse, or a qualification without a readable deadline,
is not finished. Prints a count per kind, names the unfinished records, and
exits 0 only when every record is finished. It never changes the journal.
"""

import datetime
import json
import os
import sys

REMOVED_KINDS = {"tun-binds", "image-sources", "loop-mounts"}


def revoked_and_past(record, now):
    deadline = record.get("deadline")
    if record.get("revoked") is not True or not isinstance(deadline, str):
        return False
    try:
        # Go writes RFC 3339 with nanoseconds; keep microseconds.
        stamp = deadline.replace("Z", "+00:00")
        head, _, tail = stamp.partition(".")
        if tail:
            digits = tail[: len(tail) - len(tail.lstrip("0123456789"))]
            zone = tail[len(digits):]
            stamp = f"{head}.{digits[:6]}{zone}"
        parsed = datetime.datetime.fromisoformat(stamp)
        # A deadline without a zone cannot be placed; it is not finished.
        return parsed.tzinfo is not None and parsed < now
    except ValueError:
        return False


def qualification_finished(root, rel, record, now):
    if "deadline" in record:
        return revoked_and_past(record, now)
    parts = rel.split(os.sep)
    # captures/backings/<id>.json -> captures/<id>.json -> <id>.json
    for depth in range(len(parts) - 2, -1, -1):
        owner = os.path.join(root, *parts[:depth], parts[-1])
        try:
            with open(owner, encoding="utf-8") as f:
                parent = json.load(f)
        except (OSError, ValueError):
            continue
        if isinstance(parent, dict) and "deadline" in parent:
            return revoked_and_past(parent, now)
    return False


def finished(kind, record, now):
    if kind in REMOVED_KINDS:
        return record.get("removed") is True
    if kind == "qualifications":
        return revoked_and_past(record, now)
    if kind == "helpers":
        launch = record.get("launch")
        return isinstance(launch, dict) and launch.get("resources_removed") is True
    return record.get("resources_removed") is True


def main():
    root = sys.argv[1]
    now = datetime.datetime.now(datetime.timezone.utc)
    counts, unfinished = {}, []
    for dirpath, _, files in os.walk(root):
        for name in files:
            if not name.endswith(".json"):
                continue
            path = os.path.join(dirpath, name)
            rel = os.path.relpath(path, root)
            kind = rel.split(os.sep)[0] if os.sep in rel else "processes"
            try:
                with open(path, encoding="utf-8") as f:
                    record = json.load(f)
                if not isinstance(record, dict):
                    done = False
                elif kind == "qualifications":
                    done = qualification_finished(os.path.join(root, kind), os.path.relpath(path, os.path.join(root, kind)), record, now)
                else:
                    done = finished(kind, record, now)
            except (OSError, ValueError):
                done = False
            total, closed = counts.get(kind, (0, 0))
            counts[kind] = (total + 1, closed + done)
            if not done:
                unfinished.append(rel)
    print(" ".join(f"{k}={closed}/{total}" for k, (total, closed) in sorted(counts.items())) or "no records")
    for rel in sorted(unfinished)[:20]:
        print(f"unfinished: {rel}")
    return 0 if not unfinished else 1


if __name__ == "__main__":
    sys.exit(main())
