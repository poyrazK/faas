#!/usr/bin/env python3
import csv
import json
import subprocess
import tempfile
import time
from pathlib import Path

CLI = "/opt/homebrew/bin/gregale"
PROJECT = "project-5ae37259-04cf-4070-bef"
ZONE = "europe-west3-a"
CONTROL_PLANE = "faas-control-plane"
GATEWAY = "10.156.0.5:8080"
SEQUENTIAL_APP = "public-val-go-persistent-0906"
BURST_APPS = [
    "public-val-go-persistent-0906",
    "public-val-go-oneshot-0906",
    "runtime-node24-0907",
    "runtime-python313-generation-0907",
]
BURST_COHORTS = [
    [BURST_APPS[0], BURST_APPS[1], BURST_APPS[2]],
    [BURST_APPS[0], BURST_APPS[1], BURST_APPS[3]],
    [BURST_APPS[0], BURST_APPS[2], BURST_APPS[3]],
    [BURST_APPS[1], BURST_APPS[2], BURST_APPS[3]],
]
OUTPUT = Path("/private/tmp/rc98-ssd-wakes.tsv")


def latest(slug):
    proc = subprocess.run(
        [CLI, "ps", slug, "--json"], text=True, capture_output=True, check=True
    )
    line = next(line for line in proc.stdout.splitlines() if line.strip())
    return json.loads(line)


def wait_parked(slug, expected_wake_id="", timeout=90):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        last = latest(slug)
        if last.get("state") == "parked" and (
            not expected_wake_id or last.get("wake_id") == expected_wake_id
        ):
            time.sleep(1.0)
            return last
        time.sleep(1.0)
    raise RuntimeError(f"timed out waiting for {slug} to park: {last}")


def header_value(header_path, name):
    wanted = name.lower()
    value = ""
    with open(header_path, encoding="utf-8") as stream:
        for line in stream:
            key, sep, candidate = line.partition(":")
            if sep and key.lower() == wanted:
                value = candidate.strip()
    return value


def recover_wake_id(slug, before, candidate):
    if candidate and candidate != before:
        return candidate
    deadline = time.monotonic() + 12
    while time.monotonic() < deadline:
        current = latest(slug).get("wake_id", "")
        if current and current != before:
            return current
        time.sleep(0.5)
    raise RuntimeError(f"could not correlate a new wake for {slug}; before={before}")


def sequential_request(slug):
    before = latest(slug).get("wake_id", "")
    with tempfile.NamedTemporaryFile(prefix="rc98-hdr-", delete=False) as header:
        header_path = header.name
    try:
        proc = subprocess.run(
            [
                "curl",
                "-sS",
                "--max-time",
                "20",
                "-D",
                header_path,
                "-o",
                "/dev/null",
                "-w",
                "%{http_code}\t%{time_total}",
                f"https://{slug}.gregale.dev/",
            ],
            text=True,
            capture_output=True,
            check=True,
        )
        code, duration = proc.stdout.strip().split("\t")
        wake_kind = header_value(header_path, "x-faas-wake").lower()
        wake_id = recover_wake_id(
            slug, before, header_value(header_path, "x-faas-wake-id")
        )
        if code != "200":
            raise RuntimeError(f"{slug} returned HTTP {code}")
        return wake_id, float(duration) * 1000, code, wake_kind
    finally:
        Path(header_path).unlink(missing_ok=True)


def burst_request(apps=None):
    apps = apps or BURST_APPS
    script = """set -euo pipefail
slugs='__BURST_APPS__'
rm -f /tmp/rc98-burst-*.out /tmp/rc98-burst-*.hdr
for slug in $slugs; do
  (curl -sS --max-time 20 -D "/tmp/rc98-burst-${slug}.hdr" -o /dev/null -w '%{http_code}\\t%{time_total}' -H "Host: ${slug}.gregale.dev" http://10.156.0.5:8080/ > "/tmp/rc98-burst-${slug}.out") &
done
wait
for slug in $slugs; do
  result=$(cat "/tmp/rc98-burst-${slug}.out")
  wake=$(awk -F': *' 'tolower($1)=="x-faas-wake" {gsub("\\r", "", $2); print tolower($2)}' "/tmp/rc98-burst-${slug}.hdr" | tail -1)
  wake_id=$(awk -F': *' 'tolower($1)=="x-faas-wake-id" {gsub("\\r", "", $2); print $2}' "/tmp/rc98-burst-${slug}.hdr" | tail -1)
  printf '%s\\t%s\\t%s\\t%s\\n' "$slug" "$result" "$wake" "$wake_id"
done
""".replace("__BURST_APPS__", " ".join(apps))
    proc = subprocess.run(
        [
            "gcloud",
            "compute",
            "ssh",
            CONTROL_PLANE,
            f"--project={PROJECT}",
            f"--zone={ZONE}",
            "--tunnel-through-iap",
            "--command=sudo bash -s",
        ],
        input=script,
        text=True,
        capture_output=True,
        check=True,
    )
    rows = []
    for line in proc.stdout.splitlines():
        if not line.strip():
            continue
        slug, code, duration, wake_kind, wake_id = line.split("\t")
        if slug not in apps or code != "200" or not wake_id:
            raise RuntimeError(f"invalid burst response: {line}")
        rows.append(
            {
                "app": slug,
                "wake_id": wake_id,
                "request_ms": float(duration) * 1000,
                "http_code": code,
                "wake_header": wake_kind,
            }
        )
    if {row["app"] for row in rows} != set(apps):
        raise RuntimeError(f"missing burst results: {rows}")
    return rows


def main():
    OUTPUT.unlink(missing_ok=True)
    with OUTPUT.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(
            stream,
            fieldnames=[
                "sample",
                "shape",
                "wave",
                "app",
                "wake_id",
                "request_ms_context_only",
                "http_code",
                "wake_header",
            ],
            delimiter="\t",
        )
        writer.writeheader()
        sample = 0
        wait_parked(SEQUENTIAL_APP)
        for cycle in range(1, 53):
            wake_id, duration, code, wake_kind = sequential_request(SEQUENTIAL_APP)
            sample += 1
            writer.writerow(
                {
                    "sample": sample,
                    "shape": "sequential",
                    "wave": cycle,
                    "app": SEQUENTIAL_APP,
                    "wake_id": wake_id,
                    "request_ms_context_only": f"{duration:.3f}",
                    "http_code": code,
                    "wake_header": wake_kind,
                }
            )
            stream.flush()
            print(f"sequential {cycle}/52 wake={wake_id}", flush=True)
            wait_parked(SEQUENTIAL_APP, wake_id)

        for slug in BURST_APPS:
            wait_parked(slug)
        for wave in range(1, 17):
            cohort = BURST_COHORTS[(wave - 1) % len(BURST_COHORTS)]
            rows = burst_request(cohort)
            by_app = {row["app"]: row for row in rows}
            for slug in cohort:
                row = by_app[slug]
                sample += 1
                writer.writerow(
                    {
                        "sample": sample,
                        "shape": "burst3",
                        "wave": wave,
                        "app": slug,
                        "wake_id": row["wake_id"],
                        "request_ms_context_only": f'{row["request_ms"]:.3f}',
                        "http_code": row["http_code"],
                        "wake_header": row["wake_header"],
                    }
                )
            stream.flush()
            print(
                f"burst {wave}/16 "
                + " ".join(f"{slug}={by_app[slug]['wake_id']}" for slug in cohort),
                flush=True,
            )
            for slug in cohort:
                wait_parked(slug, by_app[slug]["wake_id"])
    print(f"wrote {sample} rows to {OUTPUT}")


if __name__ == "__main__":
    main()
