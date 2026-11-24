import asyncio
import copy
import json
from pathlib import Path
from uuid import uuid4

import httpx
import pytest

from faas_sdk import JobContract, JobError
from faas_sdk.preview import (
    EVENT_PIPELINE_SCHEMA,
    EventContract,
    EventPipeline,
    EventPipelineDelivery,
    EventPipelineError,
    EventPipelineReceipt,
    PipelineScope,
    PostgresEventPipelineStore,
    event_pipeline_identity,
)
from faas_sdk.preview.event_consumer_transactions import event_consumer_request_from_headers
from faas_sdk.preview.event_types import generate_event_types

ROOT = Path(__file__).resolve().parents[1] / "request-contracts"
if not ROOT.exists():
    ROOT = Path(__file__).resolve().parents[2] / "request-contracts"
F = json.loads((ROOT / "event-pipelines.json").read_text())
SCOPE = PipelineScope.from_dict(F["scope"])
EVENT = EventContract(F["event_contract"])
CONTRACT = JobContract(F["pipeline_contract"])


def test_shared_event_type_boundaries_preserve_unicode_and_native_length():
    for value in F["event_types"]["valid"]:
        assert EventContract(F["event_contract"] | {"type": value}).spec["type"] == value
    for value in [*F["event_types"]["invalid"], "\ud800"]:
        with pytest.raises(JobError, match="invalid_contract"):
            EventContract(F["event_contract"] | {"type": value})


def delivery(body=None, headers=None):
    return EventPipelineDelivery(
        body=(F["event_body"] if body is None else body).encode(),
        headers={
            "authorization": "Bearer ingress",
            "x-gregale-event-subscription-id": F["subscription_id"],
            "x-gregale-event-acceptance-id": F["acceptance_id"],
            "x-gregale-event-source": "urn:imports",
            "x-gregale-event-id": "import-17",
            **(headers or {}),
        },
    )


class Store:
    def __init__(self):
        self.rows = {}
        self.reserve_calls = self.loads = 0
        self.lose_reserve = self.lose_complete = False

    async def load(self, identity):
        self.loads += 1
        return self.rows.get(identity)

    async def reserve(self, intent):
        self.reserve_calls += 1
        old = self.rows.get(intent["identity"])
        if old:
            return False, old
        receipt = EventPipelineReceipt.from_dict({"intent": intent, "checkpoint": None})
        self.rows[intent["identity"]] = receipt
        if self.lose_reserve:
            raise RuntimeError("private database diagnostics")
        return True, receipt

    async def complete(self, intent, checkpoint):
        receipt = EventPipelineReceipt.from_dict({"intent": intent, "checkpoint": checkpoint.to_dict()})
        self.rows[intent["identity"]] = receipt
        if self.lose_complete:
            raise RuntimeError("private database diagnostics")
        return receipt


class Native:
    def __init__(self):
        self.posts = 0
        self.lose_start = self.wrong_account = False
        self.records, self.keys = {}, {}

    async def send(self, request):
        assert request.headers["authorization"] == "Bearer platform-backend"
        path = request.url.path
        if path == "/v1/account":
            return httpx.Response(200, json={"id": str(uuid4()) if self.wrong_account else SCOPE.account_id})
        if path == "/v1/apps/imports":
            return httpx.Response(200, json={"id": SCOPE.app_id})
        if request.method == "POST":
            self.posts += 1
            key = request.headers["idempotency-key"]
            run_id = self.keys.get(key, str(uuid4()) if self.records else F["receipt"]["checkpoint"]["run_id"])
            self.records.setdefault(
                run_id,
                {
                    "id": run_id,
                    "app_id": SCOPE.app_id,
                    "workflow_name": CONTRACT.spec["name"],
                    "status": "running",
                    "input": json.loads(request.content),
                },
            )
            self.keys[key] = run_id
            if self.lose_start:
                raise httpx.ReadError("lost reply with private backend diagnostics")
            return httpx.Response(201, json=self.records[run_id])
        record = self.records.get(path.rsplit("/", 1)[-1])
        return httpx.Response(200, json=record) if record else httpx.Response(404, json={})


def setup(**overrides):
    store, native = Store(), Native()

    async def verify(value):
        if value.headers.get("authorization") != "Bearer ingress":
            raise ValueError("private signature diagnostics")
        return event_consumer_request_from_headers(value.headers, value.body, SCOPE.account_id, SCOPE.app_id)

    async def authorize(event, user, action):
        return user if user == "alice" and event.data["owner_id"] == user else None

    async def map(event, owner):
        return {"n": event.data["n"]}

    kwargs = (
        dict(
            name=F["name"],
            scope=SCOPE,
            subscription_id=F["subscription_id"],
            source="urn:imports",
            contract=CONTRACT,
            store=store,
            token="platform-backend",
            verify=verify,
            authorize=authorize,
            map=map,
            httpx_args={"transport": httpx.MockTransport(native.send)},
        )
        | overrides
    )
    return EventPipeline(EVENT, **kwargs), store, native, kwargs


@pytest.mark.asyncio
async def test_shared_contract_identity_provenance_and_exact_receipt():
    pipeline, _, native, _ = setup()
    assert await pipeline.inspect(delivery(), "alice", timeout=1) is None
    result = await pipeline.handle(delivery(), "alice", timeout=1)
    assert result.state == "started" and not result.replayed
    assert result.receipt.to_dict() == F["receipt"]
    assert native.records[F["receipt"]["checkpoint"]["run_id"]]["input"] == F["envelope"]
    assert native.posts == 1
    assert EVENT_PIPELINE_SCHEMA == (ROOT / "event-pipelines.sql").read_text()
    assert (
        event_pipeline_identity(SCOPE, F["name"], F["subscription_id"], "9007199254740993")
        == F["receipt"]["intent"]["identity"]
    )
    assert (
        event_pipeline_identity(SCOPE, F["name"], F["subscription_id"], "9007199254740992")
        != F["receipt"]["intent"]["identity"]
    )
    for language in ("typescript", "python"):
        assert generate_event_types(F["event_contract"], language) == F["generated_types"][language]
    assert "café" not in json.dumps(result.receipt.to_dict(), ensure_ascii=False)
    assert "platform-backend" not in json.dumps(result.receipt.to_dict())


@pytest.mark.asyncio
async def test_concurrent_duplicate_delivery_and_new_runtime_reuse():
    pipeline, _, native, kwargs = setup()
    results = await asyncio.gather(*(pipeline.handle(delivery(), "alice", timeout=2) for _ in range(24)))
    assert native.posts == 1
    assert sum(not r.replayed for r in results) == 1
    fresh = EventPipeline(EVENT, **kwargs)
    assert (await fresh.handle(delivery(), "alice", timeout=1)).state == "started"
    assert (await fresh.inspect(delivery(), "alice", timeout=1)).receipt.to_dict() == F["receipt"]
    assert native.posts == 1


@pytest.mark.asyncio
@pytest.mark.parametrize("stage", ["reserve", "start", "complete"])
async def test_lost_replies_do_not_redispatch_after_native_retention(stage):
    pipeline, store, native, kwargs = setup()
    store.lose_reserve = stage == "reserve"
    store.lose_complete = stage == "complete"
    native.lose_start = stage == "start"
    if stage == "start":
        assert (await pipeline.handle(delivery(), "alice", timeout=1)).state == "start_unknown"
    else:
        with pytest.raises(EventPipelineError, match="store_failed"):
            await pipeline.handle(delivery(), "alice", timeout=1)
    store.lose_reserve = store.lose_complete = native.lose_start = False
    native.keys.clear()
    fresh = EventPipeline(EVENT, **kwargs)
    repeated = await fresh.handle(delivery(), "alice", timeout=1)
    assert repeated.state == ("started" if stage == "complete" else "start_unknown")
    assert native.posts == (0 if stage == "reserve" else 1)
    if stage == "start":
        adopted = await fresh.reconcile(delivery(), F["receipt"]["checkpoint"]["run_id"], "alice", timeout=1)
        assert adopted.receipt.to_dict() == F["receipt"]
        assert native.posts == 1


@pytest.mark.asyncio
async def test_current_authority_before_store_access_including_recovery():
    revoked, actions = False, []

    async def authorize(event, user, action):
        actions.append(action)
        return user if not revoked and user == event.data["owner_id"] else None

    pipeline, store, native, _ = setup(authorize=authorize)
    with pytest.raises(EventPipelineError, match="unauthorized"):
        await pipeline.handle(delivery(), "bob", timeout=1)
    assert store.reserve_calls == native.posts == 0
    await pipeline.handle(delivery(), "alice", timeout=1)
    revoked = True
    for operation in [
        pipeline.handle(delivery(), "alice", timeout=1),
        pipeline.inspect(delivery(), "alice", timeout=1),
        pipeline.reconcile(delivery(), F["receipt"]["checkpoint"]["run_id"], "alice", timeout=1),
    ]:
        with pytest.raises(EventPipelineError, match="unauthorized"):
            await operation
    assert store.loads == 0 and store.reserve_calls == 1 and native.posts == 1
    assert "inspect" in actions and "reconcile" in actions
    with pytest.raises(EventPipelineError, match="unauthorized") as failure:
        await pipeline.handle(delivery(headers={"authorization": "Bearer attacker"}), "alice", timeout=1)
    assert "private" not in str(failure.value)


@pytest.mark.asyncio
async def test_changed_bytes_mapping_owner_and_contract_conflict():
    pipeline, _, native, kwargs = setup()
    await pipeline.handle(delivery(), "alice", timeout=1)
    for body in (F["event_body"] + " ", F["event_body"].replace('"n":1', '"n":2')):
        with pytest.raises(EventPipelineError, match="receipt_conflict"):
            await pipeline.handle(delivery(body), "alice", timeout=1)

    async def remap(event, owner):
        return {"n": 2}

    async def bob(event, user, action):
        return "bob"

    with pytest.raises(EventPipelineError, match="receipt_conflict"):
        await EventPipeline(EVENT, **(kwargs | {"map": remap})).handle(delivery(), "alice", timeout=1)
    with pytest.raises(EventPipelineError, match="receipt_conflict"):
        await EventPipeline(EVENT, **(kwargs | {"authorize": bob})).inspect(delivery(), "bob", timeout=1)
    changed = copy.deepcopy(F["event_contract"])
    changed["data"]["required"].append("label")
    with pytest.raises(EventPipelineError, match="receipt_conflict"):
        await EventPipeline(EventContract(changed), **kwargs).handle(delivery(), "alice", timeout=1)
    assert native.posts == 1


@pytest.mark.asyncio
@pytest.mark.parametrize("field", ["missing_trigger", "wrong_trigger", "owner", "input", "contract", "workflow", "run"])
async def test_reconcile_refuses_unrelated_or_changed_runs(field):
    pipeline, _, native, _ = setup()
    native.lose_start = True
    await pipeline.handle(delivery(), "alice", timeout=1)
    run_id = F["receipt"]["checkpoint"]["run_id"]
    original = copy.deepcopy(native.records[run_id])
    wrong = native.records[run_id]
    if field == "missing_trigger":
        del wrong["input"]["trigger"]
    elif field == "wrong_trigger":
        wrong["input"]["trigger"]["identity"] = "0" * 64
    elif field == "owner":
        wrong["input"]["owner_id"] = "bob"
    elif field == "input":
        wrong["input"]["input"]["n"] = 2
    elif field == "contract":
        wrong["input"]["contract_sha256"] = "0" * 64
    elif field == "workflow":
        wrong["workflow_name"] = "foreign-pipeline"
    else:
        wrong["id"] = str(uuid4())
    with pytest.raises(EventPipelineError, match="receipt_conflict"):
        await pipeline.reconcile(delivery(), run_id, "alice", timeout=1)
    native.records[run_id] = original
    assert (await pipeline.reconcile(delivery(), run_id, "alice", timeout=1)).state == "started"
    assert native.posts == 1


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "body,headers",
    [
        (None, {"x-gregale-event-acceptance-id": "01"}),
        (None, {"x-gregale-event-acceptance-id": "9223372036854775808"}),
        (None, {"X-Gregale-Event-Id": "import-17"}),
        (None, {"x-gregale-event-subscription-id": str(uuid4())}),
        (F["event_body"].replace("urn:imports", "urn:foreign"), {"x-gregale-event-source": "urn:foreign"}),
        (F["event_body"].replace("import.requested", "import.other"), {}),
        (F["event_body"].replace('"n":1', '"n":"bad"'), {}),
        ("{", {}),
        ("x" * (1024 * 1024 + 1), {}),
    ],
)
async def test_invalid_events_fail_before_admission(body, headers):
    pipeline, store, native, _ = setup()
    with pytest.raises(EventPipelineError):
        await pipeline.handle(delivery(body, headers), "alice", timeout=1)
    assert store.reserve_calls == native.posts == 0


@pytest.mark.asyncio
@pytest.mark.parametrize("phase", ["verify", "reserve"])
@pytest.mark.parametrize("mode", ["deadline", "cancelled"])
async def test_deadline_and_cancellation_fence_ignoring_callbacks(phase, mode):
    _, store, native, kwargs = setup()
    gate = asyncio.Event()

    async def wait_ignoring_cancel():
        try:
            await gate.wait()
        except asyncio.CancelledError:
            await gate.wait()

    if phase == "verify":
        original = kwargs["verify"]

        async def verify(value):
            await wait_ignoring_cancel()
            return await original(value)

        kwargs["verify"] = verify
    else:
        original = store.reserve

        async def reserve(intent):
            result = await original(intent)
            await wait_ignoring_cancel()
            return result

        store.reserve = reserve
    pipeline = EventPipeline(EVENT, **kwargs)
    pending = asyncio.create_task(pipeline.handle(delivery(), "alice", timeout=0.01 if mode == "deadline" else 1))
    if mode == "cancelled":
        await asyncio.sleep(0.005)
        pending.cancel()
    try:
        with pytest.raises(EventPipelineError if mode == "deadline" else asyncio.CancelledError):
            await pending
    finally:
        gate.set()
        await asyncio.sleep(0.02)
    assert native.posts == 0


@pytest.mark.asyncio
async def test_deadline_after_acceptance_cannot_settle_from_late_reply():
    _, store, native, kwargs = setup()
    gate, accepted = asyncio.Event(), asyncio.Event()

    async def transport(request):
        response = await native.send(request)
        if request.method == "POST":
            accepted.set()
            try:
                await gate.wait()
            except asyncio.CancelledError:
                await gate.wait()
        return response

    pipeline = EventPipeline(EVENT, **(kwargs | {"httpx_args": {"transport": httpx.MockTransport(transport)}}))
    pending = asyncio.create_task(pipeline.handle(delivery(), "alice", timeout=0.5))
    await accepted.wait()
    try:
        with pytest.raises(EventPipelineError, match="deadline"):
            await pending
    finally:
        gate.set()
        await asyncio.sleep(0.02)
    assert native.posts == 1 and next(iter(store.rows.values())).checkpoint is None
    assert (await pipeline.handle(delivery(), "alice", timeout=1)).state == "start_unknown"
    assert (
        await pipeline.reconcile(delivery(), F["receipt"]["checkpoint"]["run_id"], "alice", timeout=1)
    ).state == "started"
    assert native.posts == 1


@pytest.mark.asyncio
async def test_postgres_conditional_settlement_and_malformed_receipts():
    row, statements = None, []

    async def query(sql, values):
        nonlocal row
        statements.append(sql)
        if sql.startswith("INSERT"):
            if row:
                return []
            row = json.loads(values[1])
        if sql.startswith("UPDATE"):
            row["checkpoint"] = json.loads(values[2])
        return [{"receipt": row}] if row else []

    store = PostgresEventPipelineStore(query)
    assert await store.load(F["receipt"]["intent"]["identity"]) is None
    assert (await store.reserve(F["receipt"]["intent"]))[0]
    assert not (await store.reserve(F["receipt"]["intent"]))[0]
    checkpoint = EventPipelineReceipt.from_dict(F["receipt"]).checkpoint
    assert (await store.complete(F["receipt"]["intent"], checkpoint)).to_dict() == F["receipt"]
    assert any(
        "receipt->'intent'=$2::jsonb" in sql and "receipt->'checkpoint'='null'::jsonb" in sql for sql in statements
    )
    assert not any(any(word in sql for word in ("CREATE", "DELETE", "BEGIN", "COMMIT")) for sql in statements)
    with pytest.raises(EventPipelineError, match="receipt_conflict"):
        EventPipelineReceipt.from_dict(F["receipt"] | {"extra": True})
    with pytest.raises(EventPipelineError, match="receipt_conflict"):
        await store.complete(F["receipt"]["intent"], None)
