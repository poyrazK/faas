"""Prepare the hosted Task smoke fixture and verify an operator's observations."""
import gzip
import hashlib
import io
import json
from pathlib import Path
import stat
import tarfile
import uuid

STARTER = Path("cmd/gregale/templates/mcp-node")
ROLES = ("web", "worker")
FIXTURE_FILES = {
    "gregale-mcp.json", "gregale.yaml", "package.json", "package-lock.json", "task-limits.json",
    "server.js", "tasks-migrate.js", "tasks-release.js", "tasks-worker.js",
}
TASK_ARGUMENTS = {"report": "hosted-task-rehearsal", "steps": 20}
TASK_RESULT = f"Report {TASK_ARGUMENTS['report']} is ready after {TASK_ARGUMENTS['steps']} steps."
WORKER_MANIFEST = b"""hosting:
  start: npm run start:tasks-worker
worker:
  drain_timeout: 45s
  stop_signal: SIGTERM
  scale:
    min: 1
    max: 1
"""


def regular_bytes(path):
    if not stat.S_ISREG(path.lstat().st_mode):
        raise ValueError("expected a regular file: " + path.name)
    return path.read_bytes()


def json_bytes(value):
    return (json.dumps(value, sort_keys=True, indent=2, allow_nan=False) + "\n").encode()


def archive(files, destination):
    with destination.open("xb") as output, gzip.GzipFile(fileobj=output, mode="wb", mtime=0, filename="") as compressed:
        with tarfile.open(fileobj=compressed, mode="w|", format=tarfile.USTAR_FORMAT) as bundle:
            for name, data in sorted(files.items()):
                member = tarfile.TarInfo(name)
                member.size = len(data)
                member.mode = 0o644
                bundle.addfile(member, io.BytesIO(data))


def archive_digest(path):
    if not stat.S_ISREG(path.lstat().st_mode):
        raise ValueError("expected a regular fixture archive: " + path.name)
    digest = hashlib.sha256()
    with path.open("rb") as bundle:
        for chunk in iter(lambda: bundle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def preparation(destination):
    return {
        "version": 1, "status": "prepared", "journey": "hosted-task", "native_qualified": False,
        "archive_sha256": {role: archive_digest(destination / (role + ".tar.gz")) for role in ROLES},
        "task": {"tool": "build_report", "arguments": TASK_ARGUMENTS, "expected_result": TASK_RESULT},
    }


def prepare(repo, destination):
    starter = repo / STARTER
    names = FIXTURE_FILES | {script.name for script in starter.glob("*.js")}
    files = {name: regular_bytes(starter / name) for name in names}

    config = json.loads(files["gregale-mcp.json"])
    config["tasks"].update(enabled=True, namespace_env="MCP_TASK_NAMESPACE")
    files["gregale-mcp.json"] = json_bytes(config)

    web_package = json.loads(files["package.json"])
    web_package["scripts"]["start"] = "MCP_TASKS_ROLE=web node server.js"
    destination.mkdir(mode=0o700)
    archive({**files, "package.json": json_bytes(web_package)}, destination / "web.tar.gz")
    archive({**files, "gregale.yaml": WORKER_MANIFEST}, destination / "worker.tar.gz")
    return preparation(destination)


def unique_fields(pairs):
    result = {}
    for name, value in pairs:
        if name in result:
            raise ValueError("duplicate evidence field")
        result[name] = value
    return result


def read_document(path):
    metadata = path.lstat()
    if not stat.S_ISREG(metadata.st_mode) or metadata.st_size > 64 * 1024:
        raise ValueError("expected a bounded regular evidence file: " + path.name)
    document = json.loads(regular_bytes(path), object_pairs_hook=unique_fields)
    if not isinstance(document, dict):
        raise ValueError("expected an evidence object: " + path.name)
    return document


def identity(value, label):
    if not isinstance(value, str):
        raise ValueError("invalid " + label)
    try:
        parsed = uuid.UUID(value)
    except ValueError:
        raise ValueError("invalid " + label) from None
    if str(parsed) != value or parsed.int == 0:
        raise ValueError("invalid " + label)
    return value


def observation(receipt, phase):
    observed = receipt.get(phase)
    if not isinstance(observed, dict):
        raise ValueError("missing " + phase + " observation")
    return observed


def verify(directory):
    prepared = read_document(directory / "preparation.json")
    if json_bytes(prepared) != json_bytes(preparation(directory / "fixture")):
        raise ValueError("prepared fixture and archive digests disagree")
    receipt = read_document(directory / "hosted-task-receipt.json")
    run_id = identity(receipt.get("run_id"), "run ID")
    task_id = identity(receipt.get("task_id"), "submitted Task ID")
    initial, recovered = (observation(receipt, phase) for phase in ("initial", "recovered"))
    deployment = identity(initial.get("deployment_id"), "worker deployment ID")
    original = identity(initial.get("instance_id"), "original worker instance ID")
    replacement = identity(recovered.get("instance_id"), "replacement worker instance ID")
    if original == replacement:
        raise ValueError("worker instance did not change")

    expected = {
        "version": 1,
        "archive_sha256": prepared["archive_sha256"],
        "run_id": run_id,
        "task_id": task_id,
        "initial": {"task_id": task_id, "status": "running", "attempt": 1, "deployment_id": deployment,
                    "instance_id": original},
        "stopped_instance_id": original,
        "recovered": {"task_id": task_id, "status": "running", "attempt": 2, "deployment_id": deployment,
                      "instance_id": replacement},
        "result": {"task_id": task_id, "status": "completed", "text": TASK_RESULT},
        "observer": {"namespace": "rehearsal-" + run_id, "task_id": task_id, "attempt": 2,
                     "task_rows": 1, "completed_result_rows": 1},
        "cleanup": {"workloads_remaining": 0, "database_present": False, "logins_remaining": 0,
                    "secrets_remaining": 0, "sources_remaining": 0},
    }
    if set(receipt) != set(expected):
        raise ValueError("unexpected hosted Task receipt fields")
    for field, value in expected.items():
        if json_bytes(receipt[field]) != json_bytes(value):
            raise ValueError("hosted Task receipt failed validation: " + field)
