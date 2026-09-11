#!/usr/bin/env python3
"""Collect fifteen in-capacity two-app SSD restore waves."""

import csv
import json
import subprocess
import time
from pathlib import Path


CLI = "/opt/homebrew/bin/gregale"
APPS = [
    "beta-burst-h-0911",
    "beta-burst-i-0911",
]
OUTPUT = Path(__file__).resolve().parent / "burst-request-correlation.tsv"


def latest(slug):
    last_error = ""
    for _ in range(10):
        proc = subprocess.run(
            [CLI, "ps", slug, "--json"], text=True, capture_output=True
        )
        if proc.returncode == 0:
            lines = [line for line in proc.stdout.splitlines() if line.strip()]
            if lines:
                return json.loads(lines[0])
        last_error = proc.stderr.strip()
        time.sleep(0.5)
    raise RuntimeError(f"could not read latest instance for {slug}: {last_error}")


def wait_parked(slug, expected_wake_id="", timeout=45):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        row = latest(slug)
        if row.get("state") == "parked" and (
            not expected_wake_id or row.get("wake_id") == expected_wake_id
        ):
            return
        time.sleep(0.25)
    raise RuntimeError(f"timed out waiting for {slug} to park")


def burst_request():
    procs = [
        (
            slug,
            subprocess.Popen(
                [
                    "curl",
                    "-sS",
                    "--max-time",
                    "20",
                    "-D",
                    "-",
                    "-o",
                    "/dev/null",
                    "-w",
                    "\n%{http_code}",
                    f"https://{slug}.gregale.dev/echo",
                ],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            ),
        )
        for slug in APPS
    ]
    responses = {}
    for slug, proc in procs:
        stdout, stderr = proc.communicate(timeout=30)
        if proc.returncode != 0:
            raise RuntimeError(f"request failed for {slug}: {stderr.strip()}")
        lines = stdout.rstrip().splitlines()
        code = lines[-1]
        headers = {}
        for line in lines[:-1]:
            key, sep, value = line.partition(":")
            if sep:
                headers[key.lower()] = value.strip().rstrip("\r")
        responses[slug] = (
            code,
            headers.get("x-faas-wake-id", ""),
            headers.get("x-faas-wake", "").lower(),
        )

    rows = []
    for slug in APPS:
        code, wake_id, wake_kind = responses[slug]
        if code != "200" or not wake_id:
            raise RuntimeError(
                f"invalid public response for {slug}: code={code} wake_id={wake_id!r}"
            )
        rows.append((slug, code, wake_kind, wake_id))
    return rows


def public_request(slug, path="/echo"):
    subprocess.run(
        ["curl", "-fsS", "--max-time", "20", f"https://{slug}.gregale.dev{path}"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        text=True,
        check=True,
    )


def qualify_and_wait(rows):
    # The bounded request keeps each restored instance ready for more than the
    # configured 100 ms floor. The normal ten-second idle reaper then parks it.
    for slug in APPS:
        public_request(slug, "/warm")
    by_app = {row[0]: row for row in rows}
    for slug in APPS:
        wait_parked(slug, by_app[slug][3])
    time.sleep(1.0)


def main():
    with OUTPUT.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(
            stream,
            fieldnames=["sample", "shape", "wave", "app", "wake_id", "http_code", "wake_header"],
            delimiter="\t",
        )
        writer.writeheader()
        sample = 0
        if any(latest(slug).get("state") != "parked" for slug in APPS):
            for slug in APPS:
                public_request(slug, "/warm")
                wait_parked(slug)
        for wave in range(1, 16):
            rows = burst_request()
            by_app = {row[0]: row for row in rows}
            for slug in APPS:
                _, code, wake_kind, wake_id = by_app[slug]
                sample += 1
                writer.writerow(
                    {
                        "sample": sample,
                        "shape": "burst2",
                        "wave": wave,
                        "app": slug,
                        "wake_id": wake_id,
                        "http_code": code,
                        "wake_header": wake_kind,
                    }
                )
            stream.flush()
            print(
                f"burst {wave}/15 "
                + " ".join(f"{slug}={by_app[slug][3]}" for slug in APPS),
                flush=True,
            )
            qualify_and_wait(rows)
    print(f"wrote {sample} rows to {OUTPUT}")


if __name__ == "__main__":
    main()
