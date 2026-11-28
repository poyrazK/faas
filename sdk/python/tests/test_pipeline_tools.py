import asyncio
import json
from pathlib import Path
from types import SimpleNamespace
from uuid import uuid4

import httpx
import pytest
from faas_sdk.job_runtime import JobContract
from faas_sdk.preview import (
    PIPELINE_TOOL_SCHEMA,
    FileError,
    FileOptions,
    PipelineError,
    PipelineScope,
    PipelineStartPreviewPolicy,
    PipelineStartPreviewSource,
    PipelineTool,
    PipelineToolActor,
    PipelineToolError,
    PipelineToolMCP,
    PipelineToolMCPError,
    PipelineToolReceipt,
    pipeline_start_preview,
    read_pinned_pipeline_source,
)
from faas_sdk.preview.pipeline_gates import PipelineGateRecord, pipeline_gate_intent

ROOT = Path(__file__).resolve().parents[1] / "request-contracts"
if not ROOT.exists():
    ROOT = Path(__file__).resolve().parents[2] / "request-contracts"
F = json.loads((ROOT / "pipeline-tools.json").read_text())
SCOPE = PipelineScope.from_dict(F["scope"])
CONTRACT = JobContract(F["contract"])


class Store:
    def __init__(self):
        self.rows = {}
        self.reserves = 0
        self.lose_reserve = self.lose_complete = False

    async def load(self, id):
        return self.rows.get(id)

    async def reserve(self, intent):
        self.reserves += 1
        if intent["identity"] in self.rows:
            return False, self.rows[intent["identity"]]
        receipt = PipelineToolReceipt.from_dict(dict(intent=intent, checkpoint=None))
        self.rows[intent["identity"]] = receipt
        if self.lose_reserve:
            raise RuntimeError("private database diagnostics")
        return True, receipt

    async def complete(self, intent, checkpoint):
        old = self.rows[intent["identity"]]
        if old.checkpoint and old.checkpoint != checkpoint:
            raise PipelineToolError("receipt_conflict")
        receipt = PipelineToolReceipt.from_dict(dict(intent=intent, checkpoint=checkpoint.to_dict()))
        self.rows[intent["identity"]] = receipt
        if self.lose_complete:
            raise RuntimeError("private database diagnostics")
        return receipt


class _MissingStartBody(httpx.AsyncByteStream):
    async def __aiter__(self):
        yield b'{"status":404,"code":"workflow_run_not_found"}'


class Native:
    def __init__(self):
        self.records, self.keys = {}, {}
        self.posts = self.lookups = 0
        self.lose_start = self.wrong_account = False

    async def send(self, request):
        assert request.headers["authorization"] == "Bearer backend"
        path = request.url.path
        if path == "/v1/account":
            return httpx.Response(200, json=dict(id=str(uuid4()) if self.wrong_account else SCOPE.account_id))
        if path == "/v1/apps/imports":
            return httpx.Response(200, json=dict(id=SCOPE.app_id))
        if "/workflows/run-starts/" in path:
            assert request.method == "GET"
            self.lookups += 1
            found = self.keys.get(request.headers["idempotency-key"])
            return (
                httpx.Response(200, json={"run_id": found})
                if found
                else httpx.Response(
                    404, headers={"content-type": "application/problem+json"}, stream=_MissingStartBody()
                )
            )
        if path.endswith("/steps"):
            return httpx.Response(200, json=dict(steps=[dict(step_name="validate", status="succeeded", attempt=1)]))
        if path.endswith("/cancel"):
            record = self.records[path.split("/")[-2]]
            record.update(status="failed", cancelled_at="2026-11-17T00:00:00Z")
            return httpx.Response(200, json=record)
        if request.method == "POST":
            self.posts += 1
            key = request.headers["idempotency-key"]
            id = self.keys.get(key, str(uuid4()) if self.records else F["receipt"]["checkpoint"]["run_id"])
            self.keys[key] = id
            self.records.setdefault(
                id,
                dict(
                    id=id,
                    app_id=SCOPE.app_id,
                    workflow_name=CONTRACT.spec["name"],
                    status="running",
                    input=json.loads(request.content),
                ),
            )
            if self.lose_start:
                raise httpx.ReadError("private HTTP diagnostics")
            return httpx.Response(201, json=self.records[id])
        record = self.records.get(path.split("/")[-1])
        return httpx.Response(200 if record else 404, json=record or {})


class Gates:
    def __init__(self):
        self.gate = None
        self.expired = False

    async def list(self, run_id, owner):
        return [PipelineGateRecord.from_dict(self.gate)] if self.gate else []

    async def resolve(self, id):
        gate = (
            self.gate
            | dict(decision=dict(state="expired", actor_id=None, payload=None, decided_at="2026-11-17T00:00:00.000Z"))
            if self.expired
            else self.gate
        )
        return PipelineGateRecord.from_dict(gate)


def setup(**overrides):
    store, native, gates = Store(), Native(), Gates()
    state = dict(owner="alice", revoked=False, actions=[])

    async def authorize(request, access, action):
        state["actions"].append(action)
        return (
            PipelineToolActor(subject_id="alice-session", owner_id=state["owner"])
            if access == "alice" and not state["revoked"]
            else None
        )

    args = (
        dict(
            name=F["name"],
            description=F["description"],
            scope=SCOPE,
            store=store,
            token="backend",
            authorize=authorize,
            gate_store=gates,
            review_base_url="https://review.example.test/",
            httpx_args=dict(transport=httpx.MockTransport(native.send)),
        )
        | overrides
    )
    tool = PipelineTool(CONTRACT, **args)
    return tool, PipelineToolMCP(tool), store, native, gates, state, args


def gate(gates):
    checkpoint = F["receipt"]["checkpoint"]
    intent = pipeline_gate_intent(
        kind="approval",
        scope=SCOPE.to_dict(),
        workflow_run_id=checkpoint["run_id"],
        owner_id="alice",
        pipeline_sha256=CONTRACT.sha256,
        input_sha256=checkpoint["input_sha256"],
        name="review",
        contract_sha256="a" * 64,
        review=dict(digest="b" * 64),
        required_roles=["reviewer"],
        timeout_ms=1000,
    )
    gates.gate = dict(intent=intent, expires_at="2026-11-17T00:00:01.123Z", decision=None, delivery="pending")
    return gates.gate


@pytest.mark.asyncio
async def test_shared_schemas_envelope_receipt_and_descriptors():
    tool, mcp, store, native, *_ = setup()
    assert mcp.tools == F["tools"]
    assert PIPELINE_TOOL_SCHEMA == (ROOT / "pipeline-tools.sql").read_text()
    first = await tool.start(F["input"], F["request_id"], "alice", timeout=1)
    assert first.receipt.to_dict() == F["receipt"] and not first.replayed
    assert native.records[F["receipt"]["checkpoint"]["run_id"]]["input"] == F["envelope"]
    assert "café" not in json.dumps(first.receipt.to_dict())
    assert "backend" not in json.dumps(first.receipt.to_dict())
    descriptors = mcp.tools
    descriptors.clear()
    assert len(mcp.tools) == 5


@pytest.mark.asyncio
async def test_concurrent_and_restarted_calls_start_once():
    tool, _, store, native, _, _, args = setup()
    results = await asyncio.gather(*(tool.start(F["input"], F["request_id"], "alice", timeout=1) for _ in range(24)))
    assert native.posts == 1 and sum(not r.replayed for r in results) == 1
    native.keys.clear()
    assert (
        await PipelineTool(CONTRACT, **args).start(F["input"], F["request_id"], "alice", timeout=1)
    ).state == "started"
    assert native.posts == 1


@pytest.mark.asyncio
async def test_lost_reserve_start_and_completion_never_resubmit():
    for phase in ("reserve", "start", "complete"):
        tool, _, store, native, _, _, args = setup()
        setattr(native if phase == "start" else store, "lose_" + phase, True)
        if phase == "start":
            assert (await tool.start(F["input"], F["request_id"], "alice", timeout=1)).state == "start_unknown"
        else:
            with pytest.raises(PipelineToolError, match="store_failed"):
                await tool.start(F["input"], F["request_id"], "alice", timeout=1)
        store.lose_reserve = store.lose_complete = native.lose_start = False
        native.keys.clear()
        repeated = await PipelineTool(CONTRACT, **args).start(F["input"], F["request_id"], "alice", timeout=1)
        assert repeated.state == ("started" if phase == "complete" else "start_unknown")
        assert native.posts == (0 if phase == "reserve" else 1)
        if phase == "start":
            assert (
                await tool.reconcile(F["request_id"], F["receipt"]["checkpoint"]["run_id"], "alice", timeout=1)
            ).receipt.to_dict() == F["receipt"]


@pytest.mark.asyncio
async def test_input_contract_and_owner_changes_conflict():
    tool, _, _, native, _, state, args = setup()
    await tool.start(F["input"], F["request_id"], "alice", timeout=1)
    with pytest.raises(PipelineToolError, match="receipt_conflict"):
        await tool.start(F["input"] | dict(n=18), F["request_id"], "alice", timeout=1)
    with pytest.raises(PipelineToolError, match="receipt_conflict"):
        await PipelineTool(JobContract(F["contract"] | dict(output=dict(type="string"))), **args).inspect(
            F["request_id"], "alice", timeout=1
        )
    state["owner"] = "bob"
    with pytest.raises(PipelineToolError, match="receipt_conflict"):
        await tool.inspect(F["request_id"], "alice", timeout=1)
    assert native.posts == 1


@pytest.mark.asyncio
async def test_current_authorization_covers_every_operation():
    tool, _, store, native, _, state, _ = setup()
    with pytest.raises(PipelineToolError, match="unauthorized"):
        await tool.start(F["input"], F["request_id"], "bob", timeout=1)
    assert store.reserves == 0
    await tool.start(F["input"], F["request_id"], "alice", timeout=1)
    state["revoked"] = True
    for action in (
        lambda: tool.start(F["input"], F["request_id"], "alice", timeout=1),
        lambda: tool.inspect(F["request_id"], "alice", timeout=1),
        lambda: tool.result(F["request_id"], "alice", timeout=1),
        lambda: tool.cancel(F["request_id"], "alice", timeout=1),
        lambda: tool.reconcile(F["request_id"], F["receipt"]["checkpoint"]["run_id"], "alice", timeout=1),
    ):
        with pytest.raises(PipelineToolError, match="unauthorized"):
            await action()
    assert native.posts == 1


@pytest.mark.asyncio
async def test_approval_binding_and_expiry_are_resolved_without_deciding():
    tool, _, _, _, gates, *_ = setup()
    await tool.start(F["input"], F["request_id"], "alice", timeout=1)
    g = gate(gates)
    status = await tool.inspect(F["request_id"], "alice", timeout=1)
    assert status.state == "awaiting_approval" and status.approval.id == g["intent"]["id"]
    assert (
        status.approval.review_url
        == "https://review.example.test/?run=" + F["receipt"]["checkpoint"]["run_id"] + "&gate=" + g["intent"]["id"]
    )
    assert "review" not in status.to_dict()["approval"]
    gates.expired = True
    assert (await tool.inspect(F["request_id"], "alice", timeout=1)).state == "running"
    gates.expired = False
    base = {k: v for k, v in g["intent"].items() if k not in ("version", "id", "review_sha256", "binding_sha256")}
    gates.gate = g | dict(intent=pipeline_gate_intent(**(base | dict(owner_id="bob"))))
    with pytest.raises(PipelineToolError, match="receipt_conflict"):
        await tool.inspect(F["request_id"], "alice", timeout=1)


@pytest.mark.asyncio
async def test_mcp_status_approval_typed_result_sanitized_error_and_cancel():
    tool, mcp, _, native, gates, *_ = setup()
    args = dict(request_id=F["request_id"])

    async def call(op):
        return await mcp.call(F["name"] + "_" + op, args, "alice", timeout=1)

    assert (await call("status"))["structuredContent"]["state"] == "not_found"
    assert (await mcp.call(F["name"] + "_start", args | dict(input=F["input"]), "alice", timeout=1))[
        "structuredContent"
    ]["state"] == "started"
    gate(gates)
    assert (await call("result"))["structuredContent"]["state"] == "awaiting_approval"
    with pytest.raises(PipelineToolError, match="not_terminal"):
        await tool.result(F["request_id"], "alice", timeout=1)
    record = native.records[F["receipt"]["checkpoint"]["run_id"]]
    record.update(
        status="succeeded",
        output=dict(
            version=1, scope=SCOPE.to_dict(), owner_id="alice", contract_sha256=CONTRACT.sha256, output=F["output"]
        ),
    )
    result = await call("result")
    assert result["structuredContent"]["output"] == F["output"]
    assert json.loads(result["content"][0]["text"]) == result["structuredContent"]
    record["output"]["output"] = {"n": "bad"}
    error = await call("result")
    assert error["isError"] and "backend" not in json.dumps(error)
    record["status"] = "running"
    assert (await call("cancel"))["structuredContent"]["state"] == "cancelled"
    assert (
        await PipelineToolMCP(tool, protocol_version="2026-07-28").call(F["name"] + "_status", args, "alice", timeout=1)
    )["resultType"] == "complete"


@pytest.mark.asyncio
async def test_mcp_rejects_extra_authority_bad_inputs_and_unknown_tools():
    _, mcp, _, native, *_ = setup()
    for name, args, code in (
        (F["name"] + "_start", dict(request_id=F["request_id"], input=F["input"], owner_id="alice"), -32602),
        (F["name"] + "_start", dict(request_id=F["request_id"], input=dict(n="bad")), -32602),
        (F["name"] + "_status", dict(request_id="bad"), -32602),
        ("unknown", {}, -32601),
        (F["name"] + "_approve", dict(request_id=F["request_id"]), -32601),
    ):
        with pytest.raises(PipelineToolMCPError) as caught:
            await mcp.call(name, args, "alice", timeout=1)
        assert caught.value.code == code
    assert native.posts == 0


@pytest.mark.asyncio
async def test_reconcile_rejects_other_request_with_identical_input():
    tool, _, _, native, *_ = setup()
    native.lose_start = True
    await tool.start(F["input"], F["request_id"], "alice", timeout=1)
    native.lose_start = False
    other = await tool.start(F["input"], str(uuid4()), "alice", timeout=1)
    with pytest.raises(PipelineToolError, match="receipt_conflict"):
        await tool.reconcile(F["request_id"], other.receipt.checkpoint.run_id, "alice", timeout=1)


@pytest.mark.asyncio
async def test_deadline_and_task_cancellation_before_reservation():
    started = asyncio.Event()

    async def slow(*args):
        started.set()
        await asyncio.Event().wait()

    tool, _, store, native, *_ = setup(authorize=slow)
    with pytest.raises(PipelineToolError, match="deadline"):
        await tool.start(F["input"], F["request_id"], "alice", timeout=0.01)
    started.clear()
    task = asyncio.create_task(tool.start(F["input"], F["request_id"], "alice", timeout=1))
    await started.wait()
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task
    assert store.reserves == 0 and native.posts == 0


@pytest.mark.asyncio
async def test_late_reservation_is_fenced_after_deadline_and_cannot_dispatch():
    tool, _, store, native, _, _, args = setup()
    gate = asyncio.Event()
    original = store.reserve

    async def late(intent):
        result = await original(intent)
        try:
            await asyncio.shield(gate.wait())
        except asyncio.CancelledError:
            await gate.wait()
        return result

    store.reserve = late
    with pytest.raises(PipelineToolError, match="deadline"):
        await tool.start(F["input"], F["request_id"], "alice", timeout=0.02)
    assert len(store.rows) == 1
    gate.set()
    await asyncio.sleep(0.005)
    assert native.posts == 0
    assert (await tool.start(F["input"], F["request_id"], "alice", timeout=1)).state == "start_unknown"
    assert native.posts == 0


@pytest.mark.asyncio
async def test_mcp_snapshots_args_and_enforces_complete_frame_budget():
    tool, _, _, native, *_ = setup()
    await tool.start(F["input"], F["request_id"], "alice", timeout=1)
    record = native.records[F["receipt"]["checkpoint"]["run_id"]]
    record.update(
        status="succeeded",
        output=dict(
            version=1, scope=SCOPE.to_dict(), owner_id="alice", contract_sha256=CONTRACT.sha256, output=F["output"]
        ),
    )
    original_inspect, original_result = tool.inspect, tool.result
    entered, gate = asyncio.Event(), asyncio.Event()
    requested = []

    async def inspect(*args, **kwargs):
        entered.set()
        await gate.wait()
        return await original_inspect(*args, **kwargs)

    async def result(id, *args, **kwargs):
        requested.append(id)
        return await original_result(id, *args, **kwargs)

    tool.inspect, tool.result = inspect, result
    args = dict(request_id=F["request_id"])
    task = asyncio.create_task(PipelineToolMCP(tool).call(F["name"] + "_result", args, "alice", timeout=1))
    await entered.wait()
    args["request_id"] = str(uuid4())
    gate.set()
    assert not (await task)["isError"] and requested == [F["request_id"]]

    async def huge(*args, **kwargs):
        return "x" * 600000

    tool.result = huge
    response = await PipelineToolMCP(tool).call(
        F["name"] + "_result", dict(request_id=F["request_id"]), "alice", timeout=1
    )
    assert response["isError"] and json.loads(response["content"][0]["text"])["code"] == "limit"


@pytest.mark.asyncio
async def test_lost_start_lookup_verifies_the_original_trigger_and_input_without_attaching_or_starting():
    tool, _, _, native, *_ = setup()
    request, expected = F["request_id"], F["receipt"]["checkpoint"]["run_id"]
    native.lose_start = True
    unknown = await tool.start(F["input"], request, "alice", timeout=1)
    assert unknown.state == "start_unknown"
    key = unknown.receipt.intent["idempotency_key"]
    assert await tool.find_accepted_run(request, "alice", timeout=1) == {
        "state": "found",
        "request_id": request,
        "run_id": expected,
    }
    assert (await tool.inspect(request, "alice", timeout=1)).state == "start_unknown"
    with pytest.raises(PipelineToolError, match="unauthorized"):
        await tool.find_accepted_run(request, "bob", timeout=1)
    assert native.lookups == 1

    record = native.records[expected]
    native.records[expected] = record | {"input": record["input"] | {"input": {"n": 19}}}
    with pytest.raises(PipelineToolError, match="receipt_conflict"):
        await tool.find_accepted_run(request, "alice", timeout=1)
    native.records[expected] = record
    native.lose_start = False
    decoy = await tool.start(F["input"], str(uuid4()), "alice", timeout=1)
    native.keys[key] = decoy.receipt.checkpoint.run_id
    with pytest.raises(PipelineToolError, match="receipt_conflict"):
        await tool.find_accepted_run(request, "alice", timeout=1)

    del native.keys[key]
    assert await tool.find_accepted_run(request, "alice", timeout=1) == {
        "state": "start_unknown",
        "request_id": request,
    }
    assert (await tool.inspect(request, "alice", timeout=1)).state == "start_unknown"
    native.keys[key] = expected
    matches = await asyncio.gather(*(tool.find_accepted_run(request, "alice", timeout=1) for _ in range(2)))
    assert all(match["state"] == "found" and match["run_id"] == expected for match in matches)
    outcomes = await asyncio.gather(
        *(tool.reconcile(request, match["run_id"], "alice", timeout=1) for match in matches)
    )
    assert [result.receipt.to_dict() for result in outcomes] == [F["receipt"], F["receipt"]]
    assert native.posts == 2  # The original start and the deliberate decoy.


@pytest.mark.asyncio
async def test_retaining_revision_replays_authorized_receipt_and_fences_new_work():
    _, _, store, native, _, state, definition = setup()
    start = F["name"] + "_start"
    args = dict(request_id=F["request_id"], input=F["input"], expected_contract_sha256=CONTRACT.sha256)

    def publish(starts):
        return PipelineToolMCP(PipelineTool(CONTRACT, **definition), revision={"family": "imports", "starts": starts})

    async def failure(adapter, args, code, actor="alice"):
        result = await adapter.call(start, args, actor, timeout=1)
        assert result["isError"] is True
        assert json.loads(result["content"][0]["text"]) == {"code": code}

    current = publish("default")
    await failure(current, args | {"expected_contract_sha256": "0" * 64}, "revision_unavailable")
    assert store.reserves == 0
    native.lose_start = True
    first = await current.call(start, args, "alice", timeout=1)
    assert first["structuredContent"]["state"] == "start_unknown"
    retained = publish("retained")
    replay = await retained.call(start, args, "alice", timeout=1)
    assert replay["structuredContent"] == first["structuredContent"] | {"replayed": True}
    await failure(retained, args | {"request_id": str(uuid4())}, "revision_unavailable")
    await failure(retained, args | {"input": args["input"] | {"n": 18}}, "receipt_conflict")
    await failure(retained, args, "unauthorized", "bob")
    state["revoked"] = True
    await failure(retained, args, "unauthorized")
    assert store.reserves == native.posts == 1


@pytest.mark.asyncio
async def test_pipeline_preview_binds_current_input_caller_and_source_before_reserving(monkeypatch):
    fixture = json.loads((ROOT / "pipeline-start-preview.json").read_text())
    source, reads, revoked = fixture["source_version"], 0, False
    now, expire_during_authorization, source_read = 1_750_000_000, False, False
    monkeypatch.setattr(pipeline_start_preview, "time", SimpleNamespace(time=lambda: now))

    async def authorize(_request, actor, _action):
        nonlocal now
        if expire_during_authorization and source_read:
            now += 301
        return None if revoked else PipelineToolActor(owner_id=actor, subject_id=actor)

    async def read(_input, _access, _timeout):
        nonlocal reads, source_read
        reads += 1
        source_read = True
        return PipelineStartPreviewSource(source_version=source, summary=fixture["summary"])

    tool, _, store, native, *_ = setup(authorize=authorize)
    mcp = PipelineToolMCP(
        tool,
        preview=PipelineStartPreviewPolicy(
            contract=JobContract(fixture["contract"]),
            signing_key="local-preview-test-key" * 2,
            read=read,
        ),
    )
    args = dict(request_id=F["request_id"], input=F["input"], expected_contract_sha256=CONTRACT.sha256)

    async def call(op, arguments, actor="alice"):
        return await mcp.call(F["name"] + "_" + op, arguments, actor, timeout=2)

    async def rejected(arguments, code, actor="alice"):
        response = await call("start", arguments, actor)
        assert response["isError"] is True
        assert json.loads(response["content"][0]["text"]) == {"code": code}

    assert next(item for item in mcp.tools if item["name"].endswith("_preview"))["annotations"]["readOnlyHint"]
    shown = (await call("preview", args))["structuredContent"]
    assert shown["summary"] == fixture["summary"]
    assert store.reserves == 0
    await rejected(args, "preview_required")
    confirmed = args | {"preview_token": shown["preview_token"]}
    await rejected(confirmed, "preview_stale", "bob")
    await rejected(confirmed | {"input": F["input"] | {"n": 18}}, "preview_stale")
    await rejected(confirmed | {"request_id": str(uuid4())}, "preview_stale")
    source = fixture["changed_version"]
    await rejected(confirmed, "preview_stale")
    await rejected(confirmed | {"preview_token": "1." + "a" * 64}, "preview_stale")
    assert store.reserves == 0
    expiring = (await call("preview", args))["structuredContent"]
    source_read = False
    expire_during_authorization = True
    await rejected(args | {"preview_token": expiring["preview_token"]}, "preview_stale")
    expire_during_authorization = False
    assert store.reserves == 0
    refreshed = (await call("preview", args))["structuredContent"]
    accepted = await call("start", args | {"preview_token": refreshed["preview_token"]})
    assert accepted["structuredContent"]["state"] == "started"
    source = fixture["source_version"]
    snapshots = reads
    replayed = await call("start", confirmed)
    assert replayed["structuredContent"] == accepted["structuredContent"] | {"replayed": True}
    assert reads == snapshots
    revoked = True
    await rejected(confirmed, "unauthorized")
    assert store.reserves == native.posts == 1


@pytest.mark.asyncio
async def test_confirmation_pins_a_file_version_in_native_envelope_and_recovery_checks_it():
    fixture = json.loads((ROOT / "pipeline-start-preview.json").read_text())
    tool, _, store, native, *_ = setup()
    source = fixture["pinned_source"]

    async def read(_input, _access, _timeout):
        return PipelineStartPreviewSource(
            source_version=source["sha256"],
            summary=fixture["summary"],
            pinned_source=source,
        )

    adapter = PipelineToolMCP(
        tool,
        preview=PipelineStartPreviewPolicy(
            contract=JobContract(fixture["contract"]),
            signing_key="fixture-pinned-preview-key" * 2,
            read=read,
        ),
    )
    args = dict(request_id=F["request_id"], input=F["input"])

    async def preview():
        return (await adapter.call(F["name"] + "_preview", args, "alice", timeout=2))["structuredContent"]

    async def confirm(shown):
        return await adapter.call(
            F["name"] + "_start",
            args | {"preview_token": shown["preview_token"]},
            "alice",
            timeout=2,
        )

    original = await preview()
    assert "pinned_source" not in original
    source = source | {"version_id": fixture["replacement_version_id"]}
    stale = await confirm(original)
    assert json.loads(stale["content"][0]["text"]) == {"code": "preview_stale"}
    assert store.reserves == 0
    native.lose_start = True
    assert (await confirm(await preview()))["structuredContent"]["state"] == "start_unknown"
    run = native.records[F["receipt"]["checkpoint"]["run_id"]]
    assert run["input"]["pinned_source"] == source
    adopted = await tool.reconcile(F["request_id"], run["id"], "alice", timeout=2)
    assert adopted.receipt.intent["request_sha256"] == F["receipt"]["intent"]["input_sha256"]
    assert adopted.receipt.intent["request_sha256"] != adopted.receipt.intent["input_sha256"]
    assert (await confirm(original))["structuredContent"]["replayed"] is True
    with pytest.raises(PipelineToolError, match="receipt_conflict"):
        await tool.start(F["input"] | {"n": 18}, F["request_id"], "alice", timeout=2)
    run["input"]["pinned_source"] = fixture["pinned_source"]
    with pytest.raises(PipelineToolError, match="receipt_conflict"):
        await tool.inspect(F["request_id"], "alice", timeout=2)
    assert native.posts == 1


@pytest.mark.asyncio
async def test_pinned_reads_verify_version_and_current_authority():
    fixture = json.loads((ROOT / "pipeline-start-preview.json").read_text())
    pinned = fixture["pinned_source"]
    downloads, authorized, revoke, missing = 0, True, False, False

    async def download(key, *, version_id, options):
        nonlocal downloads, authorized
        downloads += 1
        assert key == pinned["key"]
        assert version_id == pinned["version_id"]
        if missing:
            raise FileError("request_failed", 404)
        if revoke:
            authorized = False
        return fixture["pinned_bytes"].encode()

    files = SimpleNamespace(**pinned["scope"], download=download)

    async def authorize(_source):
        return authorized

    async def read(source=pinned):
        return await read_pinned_pipeline_source(
            files,
            source,
            "alice",
            options=FileOptions(timeout=2),
            authorize=authorize,
        )

    assert await read() == fixture["pinned_bytes"].encode()
    with pytest.raises(PipelineError, match="source_unavailable"):
        await read(pinned | {"owner_id": "bob"})
    assert downloads == 1
    revoke = True
    with pytest.raises(PipelineError, match="source_forbidden"):
        await read()
    with pytest.raises(PipelineError, match="source_forbidden"):
        await read()
    assert downloads == 2
    authorized, missing = True, True
    with pytest.raises(PipelineError, match="source_unavailable"):
        await read()
    assert downloads == 3
