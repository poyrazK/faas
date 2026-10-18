"""Strict ordered Go guest-test protocol and private evidence publication."""
import datetime as dt
import json
import os
import tempfile
from pathlib import Path

MAX_IDENTITY_BYTES = 16384


def timestamp(value):
    if not isinstance(value, str):
        raise ValueError("guest_event_time_invalid")
    parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.utcoffset() != dt.timedelta(0):
        raise ValueError("guest_event_time_invalid")
    return parsed


def verify(lines, contract, identity_prefix):
    root, phases, package = contract["root"], contract["phases"], contract["package"]
    names = tuple(f"{root}/{phase}" for phase in phases)
    required = {root, *names}
    running, passed = set(), set()
    started = completed = previous = identity = None
    identity_output = None
    package_started = package_passed = False
    for line in lines:
        event = json.loads(line)
        if not isinstance(event, dict):
            raise ValueError("guest_event_invalid")
        if event.get("Action") in ("fail", "build-fail"):
            raise ValueError("guest_package_failed")
        if event.get("Package") != package:
            continue
        if package_passed:
            raise ValueError("guest_events_after_package_pass")
        name, action = event.get("Test", ""), event.get("Action", "")
        when = timestamp(event.get("Time"))
        if previous is not None and when < previous:
            raise ValueError("guest_event_time_invalid")
        previous = when
        if action == "start" and not name:
            if package_started:
                raise ValueError("guest_package_start_duplicate")
            package_started, started = True, when
        if not package_started:
            raise ValueError("guest_package_start_missing")
        if name and name not in required:
            raise ValueError("guest_unexpected_test")
        if name and action == "skip":
            raise ValueError("guest_test_failed_or_skipped")
        if name and action == "run":
            if name in running or name in passed or identity is not None:
                raise ValueError("guest_test_run_invalid")
            if name != root and (len(passed) >= len(names) or name != names[len(passed)] or running != {root}):
                expected = names[len(passed)] if len(passed) < len(names) else "none"
                raise ValueError("guest_phase_order_invalid: expected " + expected)
            running.add(name)
        elif name and action == "pass":
            if name not in running:
                raise ValueError("guest_test_pass_invalid")
            if name == root:
                if required - {root} - passed:
                    raise ValueError("guest_phases_incomplete: " + ", ".join(sorted(required - {root} - passed)))
                if identity is None:
                    raise ValueError("guest_cleanup_identity_missing")
            running.remove(name)
            passed.add(name)
        output = event.get("Output", "")
        if identity_output is not None:
            if action != "output" or name != root or identity_prefix in output:
                raise ValueError("guest_identity_interrupted")
            identity_output += output
        elif action == "output" and identity_prefix in output:
            if name != root or running != {root} or set(names) - passed or identity is not None:
                raise ValueError("guest_cleanup_identity_invalid")
            identity_output = output.split(identity_prefix, 1)[1]
        if identity_output is not None:
            # Go test2json fragments long t.Log lines. Require one complete,
            # bounded cleanup identity before the root/package can pass.
            if len(identity_output.encode("utf-8")) > MAX_IDENTITY_BYTES:
                raise ValueError("guest_identity_too_large")
            if "\n" in identity_output:
                identity = json.loads(identity_output.strip())
                identity_output = None
        if action == "pass" and not name:
            if running or passed != required:
                raise ValueError("guest_phases_incomplete: " + ", ".join(sorted(required - passed)))
            package_passed, completed = True, when
    if not package_passed or started is None or completed is None or completed <= started:
        raise ValueError("guest_package_pass_missing")
    return started, completed, identity



def write_report(path, report):
    # Refuse to replace older evidence, including an existing symlink.
    descriptor, temporary = tempfile.mkstemp(prefix=".guest-evidence-", dir=Path(path).parent)
    try:
        with os.fdopen(descriptor, "w") as output:
            json.dump(report, output, sort_keys=True, indent=2, allow_nan=False)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        os.link(temporary, path)
    finally:
        os.unlink(temporary)
