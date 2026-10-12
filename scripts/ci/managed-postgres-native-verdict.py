#!/usr/bin/env python3
"""Require every deployed PostgreSQL phase, without skipped prerequisites."""
import json
import sys

ROOT = "TestManagedPostgresNativeMetal"
PHASES = (
    "release_and_initial_restore",
    "manual_task_without_migration_secret",
    "running_rotation_and_retirement",
    "pooled_disconnect_recovery",
    "rotated_snapshot_restore",
    "parked_rotation_scheduler_recovery",
    "starter_release_and_initial_restore",
    "starter_manual_task_without_migration_secret",
    "starter_pooled_disconnect_recovery",
    "starter_failed_hook_cold_boot",
    "starter_running_rotation_and_retirement",
    "starter_parked_rotation_scheduler_recovery",
    "independent_cleanup",
)


def verify(lines):
    required = {ROOT, *(f"{ROOT}/{phase}" for phase in PHASES)}
    passed = set()
    for line in lines:
        event = json.loads(line)
        name, action = event.get("Test", ""), event.get("Action", "")
        if (name == ROOT or name.startswith(ROOT + "/")) and action in ("skip", "fail"):
            raise ValueError(f"PostgreSQL guest acceptance {action}: {name}")
        if name in required and action == "pass":
            passed.add(name)
        if not name and action == "fail":
            raise ValueError("PostgreSQL guest test package failed")
    missing = required - passed
    if missing:
        raise ValueError("PostgreSQL guest acceptance missing passes: " + ", ".join(sorted(missing)))


if __name__ == "__main__":
    try:
        with open(sys.argv[1], encoding="utf-8") as source:
            verify(source)
    except (OSError, ValueError, IndexError) as error:
        raise SystemExit(str(error)) from None
    print("All thirteen PostgreSQL probe, shipped starter, public rotation, restore, cold-boot and cleanup phases passed.")
