"""Shared native event proof, manifest-bound submission, and receipt semantics."""

import asyncio
import copy
import json
from dataclasses import asdict
from pathlib import Path
from types import SimpleNamespace

import pytest

from faas_sdk import JobCheckpoint, JobContext, JobContract, JobInput, prepare_job_input
from faas_sdk.job_runtime import job_bytes, job_hash
from faas_sdk.preview.file_pipelines import (
    FILE_PIPELINE_SCHEMA,
    FileDelivery,
    FilePipeline,
    FilePipelineError,
    FilePipelineOptions,
    FilePipelineOwner,
    FilePipelineReceipt,
    FilePipelineScope,
    NativeFileVerifier,
    PostgresFilePipelineStore,
    file_event_invocation_id,
    parse_file_created_event,
)

ROOT = Path(__file__).resolve().parents[1] / "request-contracts"
if not ROOT.exists():
    ROOT = Path(__file__).resolve().parents[2] / "request-contracts"
F = json.loads((ROOT / "file-pipelines.json").read_text())
DATA = F["source"].encode()
SCOPE = FilePipelineScope(**F["scope"])
CONTRACT = JobContract(F["contract"])
OPTIONS = FilePipelineOptions(timeout=3)


class Harness:
    def __init__(self, scenario="success"):
        self.scenario = scenario
        self.records = {}
        self.posts = self.reads = 0
        self.changed_owner = False
        self.captured = None
        payload = copy.deepcopy(F["payload"])
        if scenario == "output":
            payload["Records"][0]["s3"]["object"]["key"] = "reports/result.csv"
        self.invocation = dict(
            id=F["invocation_id"],
            account_id=SCOPE.app_id if scenario == "foreign_proof" else SCOPE.account_id,
            app_id=SCOPE.app_id,
            source="async_invoke",
            method="POST",
            path="/",
            payload=payload,
            headers={
                "x-gregale-event-id": F["event_id"],
                "x-gregale-event-source": "gregale.storage",
                "x-gregale-event-type": "object.created",
                "x-gregale-event-subscription-id": F["subscription_id"],
            },
        )
        self.delivery = FileDelivery(
            json.dumps(F["cloud_event"] if scenario == "tampered_body" else payload, ensure_ascii=False).encode(),
            F["invocation_id"],
        )
        self.files = SimpleNamespace(
            base_url=SCOPE.base_url, app_slug=SCOPE.app_slug, bucket=SCOPE.bucket, download=self.download
        )
        self.job = SimpleNamespace(submit=self.submit, resume_by_id=self.resume_by_id)
        self.args = dict(
            name="csv-files-v1",
            revision="1",
            scope=SCOPE,
            prefix="imports/",
            suffix="/input.csv",
            contract=CONTRACT,
            job=self.job,
            files=self.files,
            store=self,
            verify=NativeFileVerifier(SCOPE, self.lookup),
            authorize=self.authorize,
            map_inputs=self.map_inputs,
        )
        self.pipeline = FilePipeline(**self.args)

    async def lookup(self, id, check):
        if self.scenario == "forged_invocation":
            return {**self.invocation, "id": "77777777-7777-4777-8777-777777777777"}
        assert id == F["invocation_id"]
        return self.invocation

    async def authorize(self, event, check):
        return (
            None
            if self.scenario == "unowned"
            else FilePipelineOwner(
                "bob" if self.changed_owner else "alice",
                sha256=None if self.scenario == "version_required" else F["sha256"],
            )
        )

    async def map_inputs(self, event, data, owner, check):
        return [JobInput(F["key"].split("/")[1] + ":1", dict(row=1, email="a@example.test"))]

    async def download(self, *args, **kwargs):
        self.reads += 1
        if self.scenario == "source_missing":
            raise ValueError("private URL")
        return b"x" * len(DATA) if self.scenario == "source_changed" else DATA

    async def load(self, id, check):
        return copy.deepcopy(self.records.get(id))

    async def reserve(self, intent, check):
        if intent.identity in self.records:
            return False, copy.deepcopy(self.records[intent.identity])
        receipt = FilePipelineReceipt(intent, None)
        self.records[intent.identity] = receipt
        if self.scenario == "lost_reserve_ack":
            raise ValueError("private db failure")
        return True, receipt

    async def complete(self, intent, checkpoint, check):
        assert self.records[intent.identity].intent == intent
        receipt = FilePipelineReceipt(intent, checkpoint)
        self.records[intent.identity] = receipt
        if self.scenario == "lost_finish_ack":
            raise ValueError("private db failure")
        return receipt

    async def submit(self, inputs, *, options):
        self.posts += 1
        assert options.idempotency_key.startswith("file-pipeline:")
        refs = [asdict(await prepare_job_input(CONTRACT, i, JobContext(i.id, 0, lambda: None))) for i in inputs]
        self.captured = JobCheckpoint.from_dict(
            dict(
                version=1,
                base_url=SCOPE.base_url,
                job_name=CONTRACT.spec["name"],
                job_id=F["job_id"],
                account_id=SCOPE.account_id,
                run_id=F["run_id"],
                contract_sha256=CONTRACT.sha256,
                input_digest=job_hash(job_bytes(refs)),
                inputs=[
                    dict(
                        input_id=r["input_id"], binding_sha256=job_hash(r["input_ref"].encode()), source_task_index=None
                    )
                    for r in refs
                ],
                source_run_id=None,
            )
        )
        if self.scenario == "lost_ack":
            raise ValueError("private URL")
        return SimpleNamespace(checkpoint=self.captured)

    async def resume_by_id(self, id, *, options):
        assert id == F["run_id"]
        return SimpleNamespace(checkpoint=self.captured)


@pytest.mark.parametrize("case", F["cases"], ids=lambda c: c["scenario"])
async def test_shared_file_pipeline(case):
    h = Harness(case["scenario"])
    if case["error"]:
        with pytest.raises(FilePipelineError) as e:
            await h.pipeline.handle(h.delivery, options=OPTIONS)
        assert e.value.code == case["error"]
    else:
        assert (await h.pipeline.handle(h.delivery, options=OPTIONS)).outcome == case["outcome"]


async def test_concurrent_duplicates_and_owner_change():
    h = Harness()
    results = await asyncio.gather(*(h.pipeline.handle(h.delivery, options=OPTIONS) for _ in range(20)))
    assert sum(r.outcome == "submitted" for r in results) == 1
    assert h.posts == 1
    before = h.reads
    await h.pipeline.handle(h.delivery, options=OPTIONS)
    assert h.reads == before
    h.changed_owner = True
    with pytest.raises(FilePipelineError, match="conflict"):
        await h.pipeline.handle(h.delivery, options=OPTIONS)


async def test_uncertain_acceptance_is_never_reposted_and_exact_run_can_be_reconciled():
    h = Harness("lost_ack")
    first = await h.pipeline.handle(h.delivery, options=OPTIONS)
    assert first.outcome == "unresolved"
    assert (await h.pipeline.handle(h.delivery, options=OPTIONS)).outcome == "unresolved"
    assert h.posts == 1
    result = await h.pipeline.reconcile(first.receipt.intent.identity, F["run_id"], options=OPTIONS)
    assert result.outcome == "duplicate" and h.posts == 1
    other = Harness("lost_ack")
    first = await other.pipeline.handle(other.delivery, options=OPTIONS)
    other.captured = JobCheckpoint.from_dict({**other.captured.to_dict(), "input_digest": "f" * 64})
    with pytest.raises(FilePipelineError, match="conflict"):
        await other.pipeline.reconcile(first.receipt.intent.identity, F["run_id"], options=OPTIONS)


async def test_uncertain_reservation_and_completion_survive_restart():
    h = Harness("lost_reserve_ack")
    with pytest.raises(FilePipelineError, match="store_failed"):
        await h.pipeline.handle(h.delivery, options=OPTIONS)
    assert (await h.pipeline.handle(h.delivery, options=OPTIONS)).outcome == "unresolved" and h.posts == 0
    done = Harness("lost_finish_ack")
    result = await done.pipeline.handle(done.delivery, options=OPTIONS)
    assert result.outcome == "unresolved" and result.run_id == F["run_id"]
    assert (await FilePipeline(**done.args).handle(done.delivery, options=OPTIONS)).outcome == "duplicate"
    assert done.posts == 1


def test_native_wire_profiles_and_strict_key_decoding():
    assert file_event_invocation_id(SCOPE.account_id, F["event_id"], F["subscription_id"]) == F["invocation_id"]
    assert parse_file_created_event(F["payload"], F["event_id"], SCOPE) == parse_file_created_event(
        F["cloud_event"], F["event_id"], SCOPE
    )
    payload = copy.deepcopy(F["payload"])
    payload["Records"][0]["s3"]["object"]["key"] = "imports%2Fa+b%2B%25%2F%E7%9B%AE%E5%BD%95"
    assert parse_file_created_event(payload, F["event_id"], SCOPE).key == "imports/a b+%/目录"
    for key in ["bad%", "bad%ZZ", "bad%C0%AF"]:
        payload["Records"][0]["s3"]["object"]["key"] = key
        with pytest.raises(FilePipelineError, match="invalid_event"):
            parse_file_created_event(payload, F["event_id"], SCOPE)


async def test_replay_proof_permits_refreshed_trace_and_requires_original():
    original = Harness().invocation
    id = "77777777-7777-4777-8777-777777777777"
    replay = {**original, "id": id, "source": "replay", "headers": {**original["headers"], "traceparent": "new"}}

    async def lookup(key, check):
        return replay if key == id else original

    delivery = FileDelivery(json.dumps(F["payload"]).encode(), id)
    await NativeFileVerifier(SCOPE, lookup)(delivery, lambda: None)

    async def absent(key, check):
        return replay

    with pytest.raises(FilePipelineError, match="unverified_delivery"):
        await NativeFileVerifier(SCOPE, absent)(delivery, lambda: None)


async def test_deadlines_cancellation_and_late_reservation_are_fenced():
    for stage in ["verify", "authorize", "map_inputs"]:
        h = Harness()

        async def hang(*args):
            await asyncio.Future()

        with pytest.raises(FilePipelineError, match="deadline"):
            await FilePipeline(**{**h.args, stage: hang}).handle(h.delivery, options=FilePipelineOptions(timeout=0.02))
        assert h.posts == 0
    h = Harness()
    task = asyncio.create_task(FilePipeline(**{**h.args, "verify": hang}).handle(h.delivery, options=OPTIONS))
    await asyncio.sleep(0.01)
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task
    ready = asyncio.Event()

    class Late:
        load = h.load
        complete = h.complete

        async def reserve(self, intent, check):
            try:
                await ready.wait()
            except asyncio.CancelledError:
                await ready.wait()
            return await h.reserve(intent, check)

    with pytest.raises(FilePipelineError, match="deadline"):
        await FilePipeline(**{**h.args, "store": Late()}).handle(h.delivery, options=FilePipelineOptions(timeout=0.02))
    ready.set()
    await asyncio.sleep(0.01)
    assert h.posts == 0
    fresh = Harness()
    with pytest.raises(FilePipelineError, match="limit"):
        await fresh.pipeline.handle(fresh.delivery, options=FilePipelineOptions(timeout=3, max_bytes=1))


async def test_immutable_event_selects_exact_version():
    h = Harness("version_required")
    version = "88888888-8888-4888-8888-888888888888"
    selected = []
    event = parse_file_created_event(F["payload"], F["event_id"], SCOPE)
    from dataclasses import replace

    async def verify(*args):
        return replace(event, version_id=version)

    async def download(key, *, options, version_id):
        selected.append(version_id)
        return DATA

    h.files.download = download
    assert (
        await FilePipeline(**{**h.args, "verify": verify}).handle(h.delivery, options=OPTIONS)
    ).outcome == "submitted"
    assert selected == [version]


async def test_portable_receipt_and_postgres_schema():
    h = Harness()
    result = await h.pipeline.handle(h.delivery, options=OPTIONS)
    portable = json.loads((ROOT / "file-pipelines-portable.json").read_text())
    assert result.receipt.intent.identity == portable["identity"]
    assert result.receipt.intent.request_digest == portable["request_digest"]
    assert FilePipelineReceipt.from_dict(portable["receipt"]).to_dict() == portable["receipt"]
    assert FILE_PIPELINE_SCHEMA == (ROOT / "file-pipelines.sql").read_text()

    async def empty(sql, params):
        return []

    with pytest.raises(FilePipelineError, match="conflict"):
        await PostgresFilePipelineStore(empty).complete(result.receipt.intent, h.captured, lambda: None)


async def test_out_of_order_versions_are_exact_and_missing_old_version_is_not_current():
    from dataclasses import replace

    h = Harness()
    base = parse_file_created_event(F["payload"], F["event_id"], SCOPE)
    old = "88888888-8888-4888-8888-888888888888"
    new = "99999999-9999-4999-8999-999999999999"
    event = replace(base, version_id=new)
    versions = {old: DATA, new: DATA.replace(b"a@example", b"c@example")}
    mapped = []

    async def verify(*args):
        return event

    async def authorize(*args):
        return FilePipelineOwner("alice")

    async def download(key, *, options, version_id):
        assert version_id
        return versions[version_id]

    async def mapping(e, data, owner, check):
        mapped.append(data)
        return [JobInput(e.receipt_id + ":1", dict(row=1, email="a@example.test"))]

    h.files.download = download
    pipeline = FilePipeline(**{**h.args, "verify": verify, "authorize": authorize, "map_inputs": mapping})
    await pipeline.handle(h.delivery, options=OPTIONS)
    event = replace(base, version_id=old, receipt_id=old, event_id="multipart:" + old)
    await pipeline.handle(h.delivery, options=OPTIONS)
    assert mapped == [DATA.replace(b"a@example", b"c@example"), DATA] and h.posts == 2
    del versions[old]
    event = replace(event, receipt_id=new, event_id="multipart:" + new)
    with pytest.raises(FilePipelineError, match="source_unavailable"):
        await pipeline.handle(h.delivery, options=OPTIONS)
    assert h.posts == 2
    mutable = copy.deepcopy(F["payload"])
    mutable["Records"][0]["s3"]["object"]["versionId"] = "null"
    assert parse_file_created_event(mutable, F["event_id"], SCOPE).version_id is None


async def test_extended_scope_and_owner_metadata_are_not_persisted():
    from dataclasses import dataclass

    @dataclass(frozen=True, slots=True)
    class ExtendedScope(FilePipelineScope):
        token: str = "scope-secret"

    @dataclass(frozen=True, slots=True)
    class ExtendedOwner(FilePipelineOwner):
        token: str = "owner-secret"

    h = Harness()

    async def authorize(*args):
        return ExtendedOwner("alice", sha256=F["sha256"])

    result = await FilePipeline(**{**h.args, "scope": ExtendedScope(**F["scope"]), "authorize": authorize}).handle(
        h.delivery, options=OPTIONS
    )
    assert "secret" not in json.dumps(result.receipt.to_dict())
    assert (await h.pipeline.handle(h.delivery, options=OPTIONS)).outcome == "duplicate"


async def test_callback_delaying_timers_past_deadline_cannot_reserve():
    h = Harness()

    async def mapping(*args):
        end = asyncio.get_running_loop().time() + 0.03
        while asyncio.get_running_loop().time() < end:
            pass
        return [JobInput("cpu", dict(row=1, email="a@example.test"))]

    with pytest.raises(FilePipelineError, match="deadline"):
        await FilePipeline(**{**h.args, "map_inputs": mapping}).handle(
            h.delivery, options=FilePipelineOptions(timeout=0.01)
        )
    assert h.posts == 0 and not h.records
