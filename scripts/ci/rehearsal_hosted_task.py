"""Prepare the hosted Task smoke fixture and verify an operator's observations."""
import gzip
import hashlib
import io
import json
from pathlib import Path
import stat
import tarfile
import uuid

STARTER = Path("cmd/gregale/templates/mcp-node-tasks")
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

# Only the dedicated worker fixture emits an observation of the claim returned
# by the stock store. The lease itself must never reach the platform log.
WORKER_CLAIM_PROBE = b"""import { createHash } from 'node:crypto';
import { setTimeout } from 'node:timers/promises';
import { createPostgresMcpTaskStore } from './task-store.js';

export function createObservedStore(options) {
  const store = createPostgresMcpTaskStore(options);
  const claim = store.claim.bind(store);
  store.claim = async (maxAttempts, leaseMs, handlers, workerID) => {
    const task = await claim(maxAttempts, leaseMs, handlers, workerID);
    if (task?.tool_name === 'build_report' && task.lease_token && workerID) {
      console.log(JSON.stringify({
        event: 'gregale_hosted_task_claim',
        namespace: options.namespace,
        task_id: task.task_id,
        attempt: task.attempt_count,
        at_checkpoint: task.attempt_count === 1,
        worker_id: workerID,
        lease_sha256: createHash('sha256').update(task.lease_token, 'utf8').digest('hex'),
      }));
      // Keep attempt one at a checkpoint before the handler starts. The
      // stock 30s lease is allowed to expire normally if the VM disappears.
      if (task.attempt_count === 1) await setTimeout(15_000);
    }
    return task;
  };
  return store;
}
"""

def worker_fixture(files):
    worker = files["tasks-worker.js"]
    stock = b"startMcpTaskRuntime(config, { role: 'worker' })"
    if worker.count(stock) != 1:
        raise ValueError("worker fixture no longer has the stock Task startup")
    return {
        **files,
        "gregale.yaml": WORKER_MANIFEST,
        "rehearsal-claim.js": WORKER_CLAIM_PROBE,
        "tasks-worker.js": b"import { createObservedStore } from './rehearsal-claim.js';\n" +
            worker.replace(stock, b"startMcpTaskRuntime(config, { role: 'worker', createStore: createObservedStore })"),
    }


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


def source_digest(path):
    # Same root-first tree encoding as the hosted source review. Upload
    # normalizes regular non-executable files to mode 0600.
    digest = hashlib.sha256(b"directory:.\x00700\x00")
    with tarfile.open(path, "r:gz") as bundle:
        for member in sorted(bundle, key=lambda entry: entry.name):
            if not member.isfile() or "/" in member.name or member.mode != 0o644:
                raise ValueError("unexpected hosted Task source entry")
            digest.update(f"{member.name}\x00{member.size}\x00600\x00".encode())
            with bundle.extractfile(member) as data:
                digest.update(data.read())
    return digest.hexdigest()


def preparation(destination, include_sources=True):
    receipt = {
        "version": 1, "status": "prepared", "journey": "hosted-task", "native_qualified": False,
        "archive_sha256": {role: archive_digest(destination / (role + ".tar.gz")) for role in ROLES},
        "task": {"tool": "build_report", "arguments": TASK_ARGUMENTS, "expected_result": TASK_RESULT},
    }
    if include_sources:
        receipt["source_sha256"] = {role: source_digest(destination / (role + ".tar.gz")) for role in ROLES}
    return receipt


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
    archive(worker_fixture(files), destination / "worker.tar.gz")
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


def verify(directory, receipt_path=None):
    prepared = read_document(directory / "preparation.json")
    fixture = directory / "fixture"
    expected_preparation = preparation(fixture, include_sources=False)
    # Check archive hashes before opening a tar. Earlier receipts contain
    # only those hashes and can still be verified.
    if prepared.get("archive_sha256") != expected_preparation["archive_sha256"]:
        raise ValueError("prepared fixture and archive digests disagree")
    if "source_sha256" in prepared:
        expected_preparation["source_sha256"] = {role: source_digest(fixture / (role + ".tar.gz")) for role in ROLES}
    if json_bytes(prepared) != json_bytes(expected_preparation):
        raise ValueError("prepared fixture and archive digests disagree")
    receipt = read_document(receipt_path or directory / "hosted-task-receipt.json")
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
