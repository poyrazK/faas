import asyncio
import copy
import json
from pathlib import Path

import httpx
import pytest
from faas_sdk.job_runtime import JobContext, job_hash, prepare_job_input
from faas_sdk.preview import (
    FileOptions,
    JobPipeline,
    JobPipelineClient,
    JobPipelineOptions,
    PipelineError,
    PipelineJob,
    PipelineJobReceipt,
    PipelineScope,
    read_pipeline_artifact,
)
from faas_sdk.workflows import WORKFLOW_CODE_PROTOCOL, run_workflow_invocation

from faas_sdk import JobCheckpoint, JobContract, JobInput, JobItemOutcome, JobProgress

ROOT = Path(__file__).resolve().parents[1] / "request-contracts"
if not ROOT.exists():
    ROOT = Path(__file__).resolve().parents[2] / "request-contracts"
F = json.loads((ROOT / "job-pipelines.json").read_text())
SCOPE = PipelineScope.from_dict(F["scope"])
ROOT_CONTRACT = JobContract(F["root_contract"])
ENVELOPE = dict(version=1, contract_sha256=ROOT_CONTRACT.sha256, scope=F["scope"], owner_id="alice", input=F["input"])


class Store:
    def __init__(self):
        self.rows = {}
        self.lose_reserve = self.lose_complete = False

    async def load(self, identity):
        return self.rows.get(identity)

    async def reserve(self, intent):
        if intent["identity"] in self.rows:
            return False, self.rows[intent["identity"]]
        receipt = PipelineJobReceipt.from_dict(dict(intent=intent, checkpoint=None))
        self.rows[intent["identity"]] = receipt
        if self.lose_reserve:
            raise RuntimeError("lost ack")
        return True, receipt

    async def complete(self, intent, checkpoint):
        receipt = PipelineJobReceipt.from_dict(dict(intent=intent, checkpoint=checkpoint.to_dict()))
        self.rows[intent["identity"]] = receipt
        if self.lose_complete:
            raise RuntimeError("lost completion")
        return receipt


class Native:
    def __init__(self):
        self.posts = 0
        self.lose_accept = self.pending = self.failed = False
        self.accepted = {}
        self.outputs = {}
        self.definitions = {
            stage: self.definition(stage, index, spec) for index, (stage, spec) in enumerate(F["job_contracts"].items())
        }

    def definition(self, stage, index, spec):
        native, contract = self, JobContract(spec)

        class Handle:
            def __init__(self, checkpoint):
                self.checkpoint = checkpoint

            async def status(self, **_):
                return JobProgress(
                    run_id=self.checkpoint.run_id,
                    status="running" if native.pending else "failed" if native.failed else "succeeded",
                    total=1,
                    succeeded=0 if native.pending or native.failed else 1,
                    failed=1 if native.failed else 0,
                    cancelled=0,
                    running=1 if native.pending else 0,
                )

            async def results(self, **_):
                return tuple(
                    JobItemOutcome(
                        input_id=item.id, index=n, attempt=1, outcome="succeeded", result={"n": item.value["n"] + 1}
                    )
                    for n, item in enumerate(native.outputs[self.checkpoint.run_id])
                )

        class Job:
            async def submit(self, inputs, *, options):
                assert options.expected_base_url == SCOPE.base_url
                assert options.expected_account_id == SCOPE.account_id
                native.posts += 1
                refs = [
                    (
                        await prepare_job_input(contract, i, JobContext(input_id=i.id, attempt=0, _check=lambda: None))
                    ).to_dict()
                    for i in inputs
                ]
                checkpoint = JobCheckpoint.from_dict(
                    dict(
                        version=1,
                        base_url=SCOPE.base_url,
                        account_id=SCOPE.account_id,
                        job_id=F["job_id"],
                        job_name=spec["name"],
                        run_id=F["job_run_ids"][index],
                        contract_sha256=contract.sha256,
                        input_digest=job_hash(json.dumps(refs, separators=(",", ":")).encode()),
                        source_run_id=None,
                        inputs=[
                            dict(
                                input_id=r["input_id"],
                                binding_sha256=job_hash(r["input_ref"].encode()),
                                source_task_index=None,
                            )
                            for r in refs
                        ],
                    )
                )
                native.accepted[checkpoint.run_id] = checkpoint
                native.outputs[checkpoint.run_id] = inputs
                if native.lose_accept:
                    raise RuntimeError("uncertain POST")
                return Handle(checkpoint)

            def resume(self, checkpoint):
                return Handle(checkpoint)

            async def resume_by_id(self, run_id, **_):
                return Handle(native.accepted[run_id])

        return PipelineJob(contract=contract, job=Job())


def pipeline(store, native, **settings):
    async def run(value, wf):
        n = value["n"]
        for stage, definition in native.definitions.items():
            result = await wf.job(stage, [JobInput("one", {"n": n})], definition=definition)
            n = result.results[0].result["n"]
        return {"n": n}

    return JobPipeline(
        ROOT_CONTRACT,
        scope=SCOPE,
        store=store,
        jobs=native.definitions,
        run=run,
        options=JobPipelineOptions(**settings),
    )


async def invoke(p, journal=None):
    return await run_workflow_invocation(
        p.definition,
        dict(
            protocol=WORKFLOW_CODE_PROTOCOL,
            run_id=F["workflow_run_id"],
            workflow=ROOT_CONTRACT.spec["name"],
            input=ENVELOPE,
            invocation=1,
            journal=journal or [],
        ),
    )


def commit(journal, response):
    for op in response["ops"]:
        row = dict(
            seq=op["seq"],
            name=op["name"],
            kind=op["kind"],
            status="succeeded" if op["kind"] == "sleep" else op["status"],
            attempt=1,
        )
        if "output" in op:
            row["output"] = op["output"]
        if "error" in op:
            row["error"] = op["error"]
        if row["seq"] < len(journal):
            journal[row["seq"]] = row
        else:
            assert row["seq"] == len(journal)
            journal.append(row)


@pytest.mark.asyncio
async def test_three_stages_recover_lost_journals_and_recreated_runtimes():
    store, native, journal, dropped = Store(), Native(), [], set()
    for _ in range(40):
        r = await invoke(pipeline(store, native), journal)
        if r["result"]["status"] == "succeeded":
            assert r["result"]["output"]["output"] == F["output"]
            break
        assert r["result"]["status"] == "running"
        assert r["ops"]
        seq = r["ops"][0]["seq"]
        if seq not in dropped:
            dropped.add(seq)
        else:
            commit(journal, r)
    else:
        pytest.fail("pipeline did not finish")
    assert native.posts == 3
    assert next(iter(store.rows.values())).to_dict() == F["validate_receipt"]


@pytest.mark.asyncio
async def test_shared_node_python_journal_replays_without_repeating_completed_stage():
    receipt = PipelineJobReceipt.from_dict(F["validate_receipt"])
    assert receipt.to_dict() == F["validate_receipt"]
    store, native, journal = Store(), Native(), copy.deepcopy(F["validate_journal"])
    for _ in range(10):
        r = await invoke(pipeline(store, native), journal)
        if r["result"]["status"] == "succeeded":
            assert r["result"]["output"]["output"] == F["output"]
            break
        commit(journal, r)
    else:
        pytest.fail("pipeline did not finish")
    assert native.posts == 2


@pytest.mark.asyncio
async def test_same_stage_cannot_adopt_different_logical_inputs():
    store, native = Store(), Native()
    p = pipeline(store, native)
    await invoke(p)
    response = await run_workflow_invocation(
        p.definition,
        dict(
            protocol=WORKFLOW_CODE_PROTOCOL,
            run_id=F["workflow_run_id"],
            workflow=ROOT_CONTRACT.spec["name"],
            input=ENVELOPE | {"input": {"n": 99}},
            invocation=2,
            journal=[],
        ),
    )
    assert response["ops"][0]["status"] == "failed"
    assert response["ops"][0]["error"] == "pipeline:receipt_conflict:validate"
    assert native.posts == 1


@pytest.mark.asyncio
async def test_concurrent_drivers_and_uncertain_acceptance_reconcile_read_only():
    store, native = Store(), Native()
    native.lose_accept = True
    p = pipeline(store, native)
    a, b = await asyncio.gather(invoke(p), invoke(p))
    assert native.posts == 1
    assert a["ops"][0]["status"] == b["ops"][0]["status"] == "succeeded"
    journal = []
    commit(journal, a)
    unresolved = await invoke(p, journal)
    assert unresolved["ops"][0]["output"] == {"state": "submission_unresolved"}
    commit(journal, unresolved)
    commit(journal, await invoke(p, journal))
    with pytest.raises(PipelineError, match="receipt_conflict"):
        await p.reconcile(F["workflow_run_id"], "validate", F["job_run_ids"][0], "bob", timeout=1)
    await p.reconcile(F["workflow_run_id"], "validate", F["job_run_ids"][0], "alice", timeout=1)
    native.lose_accept = False
    for _ in range(20):
        r = await invoke(pipeline(store, native), journal)
        if r["result"]["status"] == "succeeded":
            break
        commit(journal, r)
    assert native.posts == 3


@pytest.mark.asyncio
@pytest.mark.parametrize("mode", ["reserve", "complete", "failed", "poll", "operations", "journal"])
async def test_lost_acks_limits_and_failures_bound_effects(mode):
    store, native, journal = Store(), Native(), []
    store.lose_reserve, store.lose_complete = mode == "reserve", mode == "complete"
    native.pending, native.failed = mode == "poll", mode == "failed"
    p = pipeline(
        store,
        native,
        max_polls=1,
        max_operations=1 if mode == "operations" else 900,
        max_journal_bytes=1 if mode == "journal" else 65536,
    )
    await invoke(p)
    if mode in ("reserve", "complete"):
        store.lose_reserve = store.lose_complete = False
        assert (await invoke(p))["ops"][0]["status"] == "succeeded"
    else:
        for _ in range(10):
            r = await invoke(p, journal)
            if r["result"]["status"] == "failed" or any(op.get("status") == "failed" for op in r["ops"]):
                break
            commit(journal, r)
        else:
            pytest.fail("failure was not bounded")
    assert native.posts == (0 if mode == "reserve" else 1)


@pytest.mark.asyncio
async def test_typed_client_owner_scope_results_and_cancellation():
    posts, cancelled = 0, False

    def handler(request):
        nonlocal posts, cancelled
        path = request.url.path
        if path == "/v1/account":
            return httpx.Response(200, json={"id": SCOPE.account_id})
        if path == "/v1/apps/imports":
            return httpx.Response(200, json={"id": SCOPE.app_id})
        if path.endswith("/steps"):
            return httpx.Response(200, json={"steps": []})
        if request.method == "POST":
            posts += 1
            cancelled = path.endswith("/cancel")
        return httpx.Response(
            200,
            json=dict(
                id=F["workflow_run_id"],
                app_id=SCOPE.app_id,
                workflow_name=ROOT_CONTRACT.spec["name"],
                input=ENVELOPE,
                status="succeeded",
                output={k: v for k, v in (ENVELOPE | {"output": F["output"]}).items() if k != "input"},
                **({"cancelled_at": "2026-11-15T00:00:00Z"} if cancelled else {}),
            ),
        )

    async with JobPipelineClient(
        base_url=SCOPE.base_url,
        token="alice",
        scope=SCOPE,
        owner_id="alice",
        contract=ROOT_CONTRACT,
        httpx_args={"transport": httpx.MockTransport(handler)},
    ) as alice:
        run = await alice.start(F["input"], timeout=2, idempotency_key="one")
        assert await run.result(timeout=2) == F["output"]
        assert (await alice.resume_by_id(run.checkpoint.run_id, timeout=2)).checkpoint == run.checkpoint
        async with JobPipelineClient(
            base_url=SCOPE.base_url,
            token="bob",
            scope=SCOPE,
            owner_id="bob",
            contract=ROOT_CONTRACT,
            httpx_args={"transport": httpx.MockTransport(handler)},
        ) as bob:
            with pytest.raises(PipelineError, match="scope_mismatch"):
                await bob.resume_by_id(run.checkpoint.run_id, timeout=2)
        assert posts == 1
        assert (await run.cancel(timeout=2)).cancelled
        assert posts == 2


@pytest.mark.asyncio
async def test_deadlines_fence_cancellation_ignoring_store_and_cpu_work():
    import time

    for cpu in (False, True):
        native = Native()

        class Stalled(Store):
            async def reserve(self, intent):
                if cpu:
                    time.sleep(0.02)
                else:
                    try:
                        await asyncio.sleep(0.02)
                    except asyncio.CancelledError:
                        await asyncio.sleep(0.02)
                return await super().reserve(intent)

        reply = await invoke(pipeline(Stalled(), native, request_timeout=0.005))
        assert reply["ops"][0]["status"] == "failed"
        await asyncio.sleep(0.05)
        assert native.posts == 0


@pytest.mark.asyncio
async def test_artifact_handoff_checks_owner_scope_hash_and_limits():
    from types import SimpleNamespace

    reads = 0
    raw = b"report"

    async def download(*_, **__):
        nonlocal reads
        reads += 1
        return raw

    files = SimpleNamespace(base_url=SCOPE.base_url, app_slug=SCOPE.app_slug, bucket=F["job_id"], download=download)
    artifact = dict(
        version=1,
        scope=dict(base_url=files.base_url, app_slug=files.app_slug, bucket=files.bucket),
        owner_id="alice",
        key="reports/one.csv",
        size_bytes=len(raw),
        sha256=job_hash(raw),
    )
    assert await read_pipeline_artifact(files, artifact, "alice", options=FileOptions(timeout=1)) == raw
    with pytest.raises(PipelineError, match="artifact_changed"):
        await read_pipeline_artifact(files, artifact, "bob", options=FileOptions(timeout=1))
    with pytest.raises(PipelineError, match="journal_limit"):
        await read_pipeline_artifact(files, artifact, "alice", options=FileOptions(timeout=1, max_bytes=1))
    with pytest.raises(PipelineError, match="artifact_changed"):
        await read_pipeline_artifact(files, artifact | {"sha256": "0" * 64}, "alice", options=FileOptions(timeout=1))
    assert reads == 2


@pytest.mark.asyncio
async def test_typed_start_never_retries_uncertain_mutation_and_checks_deadline_before_post():
    import time

    posts = 0

    def handler(request):
        nonlocal posts
        if request.method == "POST":
            posts += 1
            return httpx.Response(503, json={})
        return httpx.Response(200, json={"id": SCOPE.account_id if request.url.path == "/v1/account" else SCOPE.app_id})

    async with JobPipelineClient(
        base_url=SCOPE.base_url,
        token="one",
        scope=SCOPE,
        owner_id="alice",
        contract=ROOT_CONTRACT,
        httpx_args={"transport": httpx.MockTransport(handler)},
    ) as client:
        with pytest.raises(PipelineError, match="acceptance_unknown"):
            await client.start(F["input"], timeout=1, idempotency_key="one")
    assert posts == 1

    def slow(request):
        time.sleep(0.02)
        return handler(request)

    async with JobPipelineClient(
        base_url=SCOPE.base_url,
        token="one",
        scope=SCOPE,
        owner_id="alice",
        contract=ROOT_CONTRACT,
        httpx_args={"transport": httpx.MockTransport(slow)},
    ) as client:
        with pytest.raises(Exception):
            await client.start(F["input"], timeout=0.005, idempotency_key="two")
    assert posts == 1
