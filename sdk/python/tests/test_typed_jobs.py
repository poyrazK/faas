"""Portable typed job protocol, account isolation and native result semantics."""

import asyncio
import json
from pathlib import Path

import httpx
import pytest
from faas_sdk.job_runtime import JobContext, job_bytes, job_hash
from faas_sdk.job_types import generate_job_types
from faas_sdk.preview import FaaSClient as PreviewClient

from faas_sdk import (
    FaaSClient,
    JobCheckpoint,
    JobContract,
    JobError,
    JobInput,
    JobRunOptions,
    JobSubmitOptions,
    LocalJobStore,
    decode_job_output,
    prepare_job_input,
    read_job_input,
    run_job_task,
    run_local_job_batch,
)

DIRECTORY = Path(__file__).resolve().parents[1] / "request-contracts"
if not DIRECTORY.exists():
    DIRECTORY = Path(__file__).resolve().parents[2] / "request-contracts"
F = json.loads((DIRECTORY / "typed-jobs.json").read_text())
CONTRACT = JobContract(F["contract"])
INPUTS = [JobInput(i["id"], i["value"]) for i in F["inputs"]]
BASE = "https://jobs.test/prefix"


async def test_optional_submit_scope_guards_stop_before_post():
    h = Harness()
    client = FaaSClient(base_url=BASE, token="owner", httpx_args={"transport": httpx.MockTransport(h.handler)})
    try:
        job = client.define_job(CONTRACT, store=h)
        with pytest.raises(JobError, match="invalid_receipt"):
            job.submit(
                INPUTS,
                options=JobSubmitOptions(
                    timeout=2, idempotency_key="guard-base", expected_base_url="https://other.test"
                ),
            )
        with pytest.raises(JobError, match="invalid_receipt"):
            job.submit(
                INPUTS,
                options=JobSubmitOptions(
                    timeout=2, idempotency_key="guard-contract", expected_contract_sha256="0" * 64
                ),
            )
        with pytest.raises(JobError, match="invalid_response"):
            await job.submit(
                INPUTS,
                options=JobSubmitOptions(
                    timeout=2,
                    idempotency_key="guard-account",
                    expected_account_id="99999999-9999-4999-8999-999999999999",
                ),
            )
        assert h.posts == 0
        await job.submit(
            INPUTS,
            options=JobSubmitOptions(
                timeout=2, idempotency_key="guard-match", expected_base_url=BASE, expected_account_id=F["account_id"]
            ),
        )
        assert h.posts == 1
    finally:
        await client.aclose()


class Harness:
    def __init__(self, scenario="success"):
        self.scenario, self.refs, self.blobs = scenario, [], {}
        self.posts = self.reads = self.cancels = self.replays = self.signed = 0
        self.state, self.selected = "succeeded", [0, 1]

    def run(self, run_id=None):
        run_id = run_id or F["run_id"]
        return dict(
            id=run_id,
            job_id=F["job_id"],
            account_id="99999999-9999-4999-8999-999999999999" if self.scenario == "wrong_account" else F["account_id"],
            input_manifest_version=1,
            input_digest="a" * 64
            if self.scenario == "wrong_digest"
            else job_hash(job_bytes([self.refs[i] for i in self.selected])),
            tasks=len(self.selected),
            aggregate_status=self.state,
            tasks_succeeded=len(self.selected) if self.state == "succeeded" else 1 if self.state == "failed" else 0,
            tasks_failed=1 if self.state == "failed" else 0,
            tasks_cancelled=len(self.selected) if self.state == "cancelled" else 0,
            tasks_running=len(self.selected) if self.state == "running" else 0,
            **({"source_run_id": F["run_id"]} if run_id == F["replay_id"] else {}),
        )

    async def handler(self, request):
        p = request.url.path.removeprefix("/prefix")
        assert request.headers["Authorization"] == "Bearer owner"
        if p == "/v1/jobs/csv-import":
            return httpx.Response(
                200,
                json=dict(
                    id=F["job_id"],
                    account_id=F["account_id"],
                    name="other-job" if self.scenario == "wrong_job" else "csv-import",
                ),
            )
        if request.method == "POST":
            assert request.headers["Idempotency-Key"]
            if p.endswith("/cancel"):
                self.cancels += 1
                self.state = "cancelled"
                return httpx.Response(200, json={"run": self.run()})
            if p.endswith("/replay-failed"):
                self.replays += 1
                self.selected, self.state = [1], "succeeded"
                return httpx.Response(200, json=self.run(F["replay_id"]))
            self.posts += 1
            refs = json.loads(request.content)["inputs"]
            if not self.refs:
                self.refs = refs
            else:
                assert refs == self.refs
            self.state = "failed" if self.scenario == "failure" else "succeeded"
            if self.scenario == "lost_ack" and self.posts == 1:
                raise httpx.ReadError("lost response")
            return httpx.Response(200, json=self.run())
        self.reads += 1
        if p.endswith("/tasks"):
            tasks = []
            for index, i in enumerate(self.selected):
                value = dict(
                    row=F["inputs"][i]["value"]["row"],
                    saved="secret-text" if self.scenario == "wrong_output" else True,
                    rejection="none",
                )
                raw = job_bytes(
                    dict(
                        version=1,
                        contract="f" * 64 if self.scenario == "wrong_contract" else CONTRACT.sha256,
                        input_id=self.refs[i]["input_id"],
                        data=value,
                    )
                )
                sha = job_hash(raw)
                uri = ("obj://app/bucket/" if self.scenario.startswith("signed") else "s3://results/") + sha
                self.blobs[uri] = b"modified" if self.scenario == "corrupt_result" else raw
                tasks.append(
                    dict(
                        run_id=F["replay_id"] if F["replay_id"] in p else F["run_id"],
                        task_index=index,
                        input_id="other" if self.scenario == "wrong_input" else self.refs[i]["input_id"],
                        input_ref="unknown" if self.scenario == "wrong_binding" else self.refs[i]["input_ref"],
                        **({"source_task_index": i} if F["replay_id"] in p else {}),
                        status="cancelled"
                        if self.state == "cancelled"
                        else "failed"
                        if self.scenario == "failure" and F["replay_id"] not in p and i == 1
                        else "succeeded",
                        attempt=1,
                        output_manifest={
                            "version": 1,
                            "artifacts": [
                                {
                                    "name": "result",
                                    "uri": "https://unsafe.test/secret" if self.scenario == "unsafe_uri" else uri,
                                    "size_bytes": len(raw),
                                    "sha256": "sha256:" + sha,
                                }
                            ],
                        },
                    )
                )
            if self.scenario == "duplicate_task":
                tasks = [tasks[0], tasks[0]]
            return httpx.Response(
                200,
                json=dict(
                    tasks=tasks,
                    total=3 if self.scenario == "wrong_total" else len(self.selected),
                    offset=0,
                    limit=200,
                    next_offset=0 if self.scenario == "bad_offset" else -1,
                ),
            )
        if p.endswith("/download"):
            self.signed += 1
            raw = list(self.blobs.values())[self.signed - 1]
            return httpx.Response(
                200,
                json=dict(
                    name="result",
                    size_bytes=1 if self.scenario == "signed_mismatch" else len(raw),
                    sha256="sha256:" + job_hash(raw),
                    download=dict(
                        url="https://signed.test/blob?secret=capability",
                        method="GET",
                        headers={"X-Object-Proof": "object-only"},
                        expires_at="2020-01-01T00:00:00Z"
                        if self.scenario == "signed_expired"
                        else "2099-01-01T00:00:00Z",
                    ),
                ),
            )
        return httpx.Response(200, json=self.run(F["replay_id"] if F["replay_id"] in p else None))

    async def put(self, data, sha256, context):
        uri = "s3://results/" + sha256
        self.blobs[uri] = data
        return uri

    async def get(self, uri, context):
        assert not uri.startswith("https:")
        return self.blobs[uri]


@pytest.mark.parametrize("factory", [FaaSClient, PreviewClient], ids=["public", "preview"])
@pytest.mark.parametrize("case", F["cases"], ids=lambda c: c["scenario"])
async def test_shared_jobs(factory, case, monkeypatch):
    h = Harness(case["scenario"])
    client = factory(base_url=BASE, token="owner", httpx_args={"transport": httpx.MockTransport(h.handler)})
    original_client = httpx.AsyncClient

    def data_client(*args, **kwargs):
        async def fetch(request):
            assert request.url.host == "signed.test"
            assert "Authorization" not in request.headers
            assert request.headers["X-Object-Proof"] == "object-only"
            return httpx.Response(200, content=list(h.blobs.values())[h.signed - 1])

        return original_client(*args, **kwargs, transport=httpx.MockTransport(fetch))

    async def work():
        job = client.define_job(CONTRACT, store=h)
        run = await job.submit(INPUTS, options=JobSubmitOptions(timeout=2, idempotency_key="submission-1"))
        assert "example.test" not in json.dumps(run.checkpoint.to_dict())
        resumed = job.resume(JobCheckpoint.from_dict(json.loads(json.dumps(run.checkpoint.to_dict()))))
        if case["scenario"] == "success":
            assert (
                await job.resume_by_id(run.checkpoint.run_id, options=JobRunOptions(timeout=2))
            ).checkpoint == run.checkpoint
        assert (await resumed.wait(options=JobRunOptions(timeout=2, max_polls=1))).total == 2
        # Only replace the data client; configuration snapshots still need the real constructor.
        if case["scenario"].startswith("signed"):
            import faas_sdk._typed_jobs as module

            # The snapshot helper creates its client before the coroutine begins.
            pending = resumed.results(options=JobRunOptions(timeout=2))
            with monkeypatch.context() as patch:
                patch.setattr(module.httpx, "AsyncClient", data_client)
                result = await pending
        else:
            result = await resumed.results(options=JobRunOptions(timeout=2))
        assert result[1].outcome == case["outcome"]
        if case["outcome"] == "succeeded":
            assert result[0].result == {"row": 1, "saved": True, "rejection": "none"}

    try:
        if case["error"]:
            with pytest.raises(JobError) as error:
                await work()
            assert error.value.code == case["error"]
        else:
            await work()
        assert h.posts <= 1
        if case["scenario"] == "wrong_job":
            assert h.posts == 0
    finally:
        await client.aclose()
        client.close()


async def test_replay_cancel_wait_and_recovery():
    h = Harness("failure")
    client = FaaSClient(base_url=BASE, token="owner", httpx_args={"transport": httpx.MockTransport(h.handler)})
    try:
        run = await client.define_job(CONTRACT, store=h).submit(
            INPUTS, options=JobSubmitOptions(timeout=1, idempotency_key="run")
        )
        replay = await run.retry_failed(options=JobRunOptions(timeout=1), idempotency_key="replay")
        assert (
            await client.define_job(CONTRACT, store=h).resume_by_id(
                replay.checkpoint.run_id, options=JobRunOptions(timeout=1)
            )
        ).checkpoint == replay.checkpoint
        assert replay.checkpoint.source_run_id == run.checkpoint.run_id
        assert [i.input_id for i in replay.checkpoint.inputs] == ["row-2"]
        assert (await replay.results(options=JobRunOptions(timeout=1)))[0].result["row"] == 2
        h.state, h.selected = "queued", [0, 1]
        with pytest.raises(JobError) as error:
            await run.wait(options=JobRunOptions(timeout=1, max_polls=2, poll_delay=0))
        assert error.value.code == "poll_limit" and h.cancels == 0
        with pytest.raises(JobError) as error:
            await run.wait(options=JobRunOptions(timeout=0.02, poll_delay=1))
        assert error.value.code == "deadline" and h.cancels == 0
        assert (await run.cancel(options=JobRunOptions(timeout=1))).status == "cancelled"
        assert h.cancels == h.replays == 1
    finally:
        await client.aclose()
        client.close()


async def test_lost_acceptance_needs_explicit_same_key_resubmission():
    h = Harness("lost_ack")
    client = FaaSClient(base_url=BASE, token="owner", httpx_args={"transport": httpx.MockTransport(h.handler)})
    try:
        job = client.define_job(CONTRACT)
        options = JobSubmitOptions(timeout=1, idempotency_key="same")
        with pytest.raises(JobError) as error:
            await job.submit(INPUTS, options=options)
        assert error.value.code == "acceptance_unknown" and h.posts == 1
        assert (await job.submit(INPUTS, options=options)).checkpoint.run_id == F["run_id"]
        assert h.posts == 2
    finally:
        await client.aclose()
        client.close()


def test_schema_and_portable_fingerprint():
    for value in (
        {"row": 1, "email": "x"},
        {"row": 1, "email": "abc", "extra": True},
        {"row": float("nan"), "email": "abc"},
        {"row": 2**53, "email": "abc"},
    ):
        with pytest.raises(JobError) as error:
            CONTRACT.input(value)
        assert error.value.code == "invalid_input"
    with pytest.raises(JobError):
        JobContract({**F["contract"], "input": {**F["contract"]["input"], "pattern": "ignored"}})
    assert 'readonly "row": number' in generate_job_types(F["contract"], "typescript")
    assert "NotRequired[bool]" in generate_job_types(F["contract"], "python")
    portable = json.loads((DIRECTORY / "typed-jobs-portable.json").read_text())
    assert CONTRACT.sha256 == portable["contract_sha256"]


async def test_local_execution_retry_concurrency_and_deadline():
    active = peak = 0

    async def handler(input, context):
        nonlocal active, peak
        active += 1
        peak = max(peak, active)
        await asyncio.sleep(0.002)
        active -= 1
        if input["row"] == 2 and context.attempt == 1:
            raise RuntimeError("secret failure")
        return dict(row=input["row"], saved=True, rejection="none")

    result = await run_local_job_batch(CONTRACT, INPUTS, handler, timeout=1, concurrency=1, max_attempts=2)
    assert peak == 1 and [r.attempts for r in result] == [1, 2]

    async def slow(*_):
        await asyncio.Future()

    with pytest.raises(JobError) as error:
        await run_local_job_batch(CONTRACT, INPUTS, slow, timeout=0.02)
    assert error.value.code == "deadline"


async def test_worker_and_external_payload_integrity(tmp_path):
    store, context = LocalJobStore(tmp_path / "blobs"), JobContext("large", 1, lambda: None)
    input = JobInput("large", {"row": 1, "email": "x" * 5000})
    binding = await prepare_job_input(CONTRACT, input, context, store)
    assert len(binding.input_ref) <= 2048
    assert await read_job_input(CONTRACT, binding, context, store) == input.value
    manifest = tmp_path / "manifest.json"
    env = dict(
        GREGALE_INPUT_ID=binding.input_id,
        GREGALE_INPUT_REF=binding.input_ref,
        GREGALE_TASK_ATTEMPT="1",
        GREGALE_TASK_INDEX="0",
        GREGALE_TASK_COUNT="1",
        GREGALE_RUN_ID=F["run_id"],
        GREGALE_OUTPUT_MANIFEST_PATH=str(manifest),
    )
    result = await run_job_task(
        CONTRACT,
        lambda *_: dict(row=1, saved=True, rejection="none"),
        timeout=1,
        store=store,
        env=env,
        manage_signals=False,
    )
    assert result["saved"] is True
    artifact = json.loads(manifest.read_text())["artifacts"][0]
    raw = await store.get(artifact["uri"], context)
    assert artifact["sha256"] == "sha256:" + job_hash(raw)
    assert decode_job_output(CONTRACT, "large", raw)["saved"] is True
    with pytest.raises(JobError):
        decode_job_output(CONTRACT, "other", raw)
    portable = json.loads((DIRECTORY / "typed-jobs-portable.json").read_text())
    from faas_sdk import JobInputBinding

    assert await read_job_input(CONTRACT, JobInputBinding(**portable["binding"]), context) == portable["payload"]


async def test_snapshot_accounts_and_task_cancellation():
    h, seen = Harness(), []

    async def transport(request):
        token = request.headers["Authorization"]
        seen.append(token)
        await asyncio.sleep(0.001)
        return await h.handler(request) if token == "Bearer owner" else httpx.Response(404, json={"detail": "secret"})

    a, b = [
        FaaSClient(base_url=BASE, token=token, httpx_args={"transport": httpx.MockTransport(transport)})
        for token in ("owner", "foreign")
    ]
    try:
        job = a.define_job(CONTRACT)
        pending = job.submit(INPUTS, options=JobSubmitOptions(timeout=1, idempotency_key="run"))
        a.async_httpx_client.headers["Authorization"] = "Bearer changed"
        run = await pending
        assert "Bearer changed" not in seen
        with pytest.raises(JobError) as error:
            await b.define_job(CONTRACT).resume(run.checkpoint).status(options=JobRunOptions(timeout=1))
        assert error.value.code == "request_failed" and h.posts == 1
        a.async_httpx_client.headers["Authorization"] = "Bearer owner"
        h.state = "queued"
        task = asyncio.create_task(run.wait(options=JobRunOptions(timeout=1)))
        await asyncio.sleep(0.005)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        assert h.cancels == 0
        with pytest.raises(JobError):
            await job.submit(INPUTS, options=JobSubmitOptions(timeout=1, idempotency_key="limited", max_requests=1))
        assert h.posts == 1
    finally:
        await a.aclose()
        await b.aclose()
        a.close()
        b.close()


async def test_stalled_reads_progress_and_storage_share_deadline():
    class Slow(httpx.AsyncByteStream):
        async def __aiter__(self):
            yield b"{"
            await asyncio.Future()

    client = FaaSClient(
        base_url=BASE, httpx_args={"transport": httpx.MockTransport(lambda _: httpx.Response(200, stream=Slow()))}
    )
    try:
        with pytest.raises(JobError) as error:
            await client.define_job(CONTRACT).submit(
                INPUTS, options=JobSubmitOptions(timeout=0.02, idempotency_key="bounded")
            )
        assert error.value.code == "deadline"
    finally:
        await client.aclose()
        client.close()
    release, ended = asyncio.Event(), asyncio.Event()

    class SlowStore:
        async def put(self, data, sha, context):
            try:
                await release.wait()
            except asyncio.CancelledError:
                await release.wait()
            ended.set()
            return "s3://results/large"

        async def get(self, uri, context):
            return b""

    h = Harness()
    client = FaaSClient(base_url=BASE, token="owner", httpx_args={"transport": httpx.MockTransport(h.handler)})
    try:
        with pytest.raises(JobError) as error:
            await client.define_job(CONTRACT, store=SlowStore()).submit(
                [JobInput("large", {"row": 1, "email": "x" * 5000})],
                options=JobSubmitOptions(timeout=0.02, idempotency_key="large"),
            )
        assert error.value.code == "deadline"
        release.set()
        await asyncio.wait_for(ended.wait(), 1)
        await asyncio.sleep(0)
        assert h.posts == 0
        run = await client.define_job(CONTRACT).submit(
            INPUTS, options=JobSubmitOptions(timeout=1, idempotency_key="ready")
        )

        async def progress(_):
            await asyncio.Future()

        with pytest.raises(JobError) as error:
            await run.wait(options=JobRunOptions(timeout=0.02, on_progress=progress))
        assert error.value.code == "deadline"
    finally:
        release.set()
        await client.aclose()
        client.close()


async def test_native_deadline_prevents_late_manifest_publication(tmp_path):
    context = JobContext("row-1", 1, lambda: None)
    binding = await prepare_job_input(CONTRACT, INPUTS[0], context)
    release, ended = asyncio.Event(), asyncio.Event()

    async def late(*_):
        try:
            await release.wait()
        except asyncio.CancelledError:
            await release.wait()
        ended.set()
        return {"row": 1, "saved": True, "rejection": "none"}

    manifest = tmp_path / "manifest"
    env = dict(
        GREGALE_RUN_ID=F["run_id"],
        GREGALE_TASK_INDEX="0",
        GREGALE_TASK_COUNT="1",
        GREGALE_TASK_ATTEMPT="1",
        GREGALE_INPUT_ID=binding.input_id,
        GREGALE_INPUT_REF=binding.input_ref,
        GREGALE_OUTPUT_MANIFEST_PATH=str(manifest),
    )
    with pytest.raises(JobError) as error:
        await run_job_task(
            CONTRACT, late, timeout=0.02, store=LocalJobStore(tmp_path / "blobs"), manage_signals=False, env=env
        )
    assert error.value.code == "deadline"
    release.set()
    await asyncio.wait_for(ended.wait(), 1)
    await asyncio.sleep(0)
    assert not manifest.exists()
