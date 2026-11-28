import asyncio
import copy
import json
from datetime import UTC, datetime, timedelta
from pathlib import Path

import httpx
import pytest
from faas_sdk.job_runtime import job_value_hash
from faas_sdk.preview import (
    PIPELINE_GATE_SCHEMA,
    JobPipeline,
    NativePipelineGateTransport,
    PipelineGateActor,
    PipelineGateError,
    PipelineGateOptions,
    PipelineGateRecord,
    PipelineGateService,
    PipelineScope,
    PostgresPipelineGateStore,
    pipeline_gate_intent,
)
from faas_sdk.preview.live_jobs import LiveJobSnapshot
from faas_sdk.workflows import WORKFLOW_CODE_PROTOCOL, run_workflow_invocation

from faas_sdk import JobContract, JobInput

ROOT = Path(__file__).resolve().parents[1] / "request-contracts"
if not ROOT.exists():
    ROOT = Path(__file__).resolve().parents[2] / "request-contracts"
F = json.loads((ROOT / "pipeline-gates.json").read_text())
SCOPE = PipelineScope.from_dict(F["scope"])
CONTRACT, PIPELINE = JobContract(F["contract"]), JobContract(F["root_contract"])
INTENT = F["intent"]


def iso(value):
    return value.isoformat(timespec="milliseconds").replace("+00:00", "Z")


class Store:
    def __init__(self):
        self.rows = {}
        self.clock = datetime(2026, 11, 15, 12, tzinfo=UTC)

    async def resolve(self, identity):
        r = self.rows.get(identity)
        if r and r.decision is None and datetime.fromisoformat(r.expires_at.replace("Z", "+00:00")) <= self.clock:
            r = PipelineGateRecord.from_dict(
                r.to_dict()
                | {"decision": dict(state="expired", actor_id=None, payload=None, decided_at=iso(self.clock))}
            )
            self.rows[identity] = r
        return r

    async def open(self, intent):
        old = await self.resolve(intent["id"])
        if old:
            if old.intent != intent:
                raise PipelineGateError("conflict")
            return old
        r = PipelineGateRecord.from_dict(
            dict(
                intent=intent,
                expires_at=iso(self.clock + timedelta(milliseconds=intent["timeout_ms"])),
                decision=None,
                delivery="pending",
            )
        )
        self.rows[intent["id"]] = r
        return r

    async def decide(self, identity, binding, proposal):
        r = await self.resolve(identity)
        assert r and r.intent["binding_sha256"] == binding
        if r.decision:
            return r
        r = PipelineGateRecord.from_dict(r.to_dict() | {"decision": proposal | {"decided_at": iso(self.clock)}})
        self.rows[identity] = r
        return r

    async def mark(self, identity, decision, delivery):
        r = self.rows[identity]
        assert r.decision == decision
        self.rows[identity] = PipelineGateRecord.from_dict(r.to_dict() | {"delivery": delivery})

    async def pending(self, limit, scope, pipeline, contract):
        return [
            r
            for r in self.rows.values()
            if r.delivery == "pending"
            and r.intent["scope"] == scope.to_dict()
            and r.intent["pipeline_sha256"] == pipeline
            and r.intent["contract_sha256"] == contract
        ][:limit]

    async def list(self, run_id, owner_id):
        return [
            r for r in self.rows.values() if r.intent["workflow_run_id"] == run_id and r.intent["owner_id"] == owner_id
        ]


class Transport:
    def __init__(self):
        self.is_active, self.lose_ack, self.events = True, False, []

    async def active(self, _):
        return self.is_active

    async def publish(self, intent):
        self.events.append(dict(key="pipeline-gate:" + intent["id"], binding=intent["binding_sha256"]))
        if self.lose_ack:
            raise RuntimeError("lost native ack")


async def authorize(_record, _action, user):
    return PipelineGateActor(actor_id=user, owner_id="alice" if user == "carol" else user, roles=("reviewer",))


def service(store, transport, auth=authorize):
    return PipelineGateService(
        scope=SCOPE,
        pipeline_sha256=PIPELINE.sha256,
        contract=CONTRACT,
        store=store,
        transport=transport,
        authorize=auth,
    )


async def decide(s, state="approved", user="alice", intent=INTENT, payload=None):
    return await s.decide(
        intent["id"],
        binding_sha256=intent["binding_sha256"],
        state=state,
        auth=user,
        payload=payload if payload is not None else {"reason": "checked"},
        timeout=1,
    )


def pipeline(store, ignored=False):
    async def run(_value, wf):
        outcome = await wf.approval(
            "review", CONTRACT, INTENT["review"], options=PipelineGateOptions(timeout=60, required_roles=("reviewer",))
        )
        if ignored:
            await wf.job("import", [JobInput("row", INTENT["review"])], definition=None)
        return {"n": 1 if outcome.state == "approved" else 0}

    return JobPipeline(PIPELINE, scope=SCOPE, store=None, gate_store=store, jobs={}, run=run)


async def invoke(p, journal=None):
    return await run_workflow_invocation(
        p.definition,
        dict(
            protocol=WORKFLOW_CODE_PROTOCOL,
            workflow=PIPELINE.spec["name"],
            run_id=INTENT["workflow_run_id"],
            input=F["envelope"],
            invocation=len(journal or []) + 1,
            journal=journal or [],
        ),
    )


def commit(journal, reply):
    for op in reply["ops"]:
        assert op["seq"] == len(journal)
        journal.append(op | {"status": "succeeded" if op["kind"] == "event" else op["status"], "attempt": 1})


@pytest.mark.asyncio
async def test_shared_intent_schema_and_node_journal_replay():
    base = {k: v for k, v in INTENT.items() if k not in ("version", "id", "review_sha256", "binding_sha256")}
    assert pipeline_gate_intent(**base) == INTENT
    assert PipelineGateRecord.from_dict(F["record"]).to_dict() == F["record"]
    assert PIPELINE_GATE_SCHEMA == (ROOT / "pipeline-gates.sql").read_text()
    reply = await invoke(pipeline(Store()), F["journal"])
    assert reply["result"]["status"] == "succeeded"
    assert reply["result"]["output"]["output"] == {"n": 1}


@pytest.mark.asyncio
async def test_dropped_journals_early_forged_events_and_restart():
    store, transport, journal = Store(), Transport(), []
    first = await invoke(pipeline(store))
    assert first["ops"] == (await invoke(pipeline(store)))["ops"]
    commit(journal, first)
    commit(journal, await invoke(pipeline(store), journal))
    commit(journal, await invoke(pipeline(store), journal))
    assert journal[-1].get("output") is None
    waiting = await invoke(pipeline(store), journal)
    assert waiting["ops"][0]["kind"] == "event"
    await decide(service(store, transport))
    commit(journal, waiting)
    resolved = await invoke(pipeline(store), journal)
    assert resolved["ops"] == (await invoke(pipeline(store), journal))["ops"]
    commit(journal, resolved)
    assert (await invoke(pipeline(store), journal))["result"]["status"] == "succeeded"
    assert len(store.rows) == 1


@pytest.mark.asyncio
async def test_current_auth_and_delegated_roles_including_retries():
    store, transport = Store(), Transport()
    await store.open(INTENT)
    s = service(store, transport)
    with pytest.raises(PipelineGateError, match="forbidden"):
        await s.inspect(INTENT["id"], "bob", timeout=1)
    with pytest.raises(PipelineGateError, match="forbidden"):
        await decide(s, user="bob")
    assert (await decide(s, user="carol")).decision.actor_id == "carol"

    async def revoked(*_):
        return PipelineGateActor(actor_id="carol", owner_id="alice", roles=())

    with pytest.raises(PipelineGateError, match="forbidden"):
        await decide(service(store, transport, revoked), user="carol")


@pytest.mark.asyncio
async def test_opposing_decisions_and_delivery_recovery_after_new_service():
    store, transport = Store(), Transport()
    await store.open(INTENT)
    transport.lose_ack = True
    choices = await asyncio.gather(
        decide(service(store, transport)), decide(service(store, transport), "rejected"), return_exceptions=True
    )
    assert sum(not isinstance(c, Exception) for c in choices) == 1
    first = await decide(service(store, transport))
    assert first.delivery == "pending"
    transport.lose_ack = False
    assert await service(store, transport).drain(10, timeout=1) == 1
    assert transport.events[0] == transport.events[-1]
    assert (await decide(service(store, transport))).decision == first.decision
    with pytest.raises(PipelineGateError, match="conflict"):
        await decide(service(store, transport), user="carol")


@pytest.mark.asyncio
async def test_expiry_after_auth_and_parent_cancellation():
    store, transport = Store(), Transport()
    await store.open(INTENT)

    async def late(*_):
        store.clock += timedelta(minutes=1)
        return PipelineGateActor(actor_id="alice", owner_id="alice", roles=("reviewer",))

    with pytest.raises(PipelineGateError, match="conflict"):
        await decide(service(store, transport, late))
    assert store.rows[INTENT["id"]].decision.state == "expired"
    store = Store()
    await store.open(INTENT)
    transport.is_active = False
    with pytest.raises(PipelineGateError, match="inactive"):
        await decide(service(store, transport))
    await service(store, transport).drain(10, timeout=1)
    assert store.rows[INTENT["id"]].decision.state == "cancelled"
    assert store.rows[INTENT["id"]].delivery == "closed"
    assert not transport.events


@pytest.mark.asyncio
async def test_changed_review_conflicts_and_ignored_rejections_block_jobs():
    store = Store()
    await store.open(INTENT)
    base = {k: v for k, v in INTENT.items() if k not in ("version", "id", "review_sha256", "binding_sha256")}
    changed = pipeline_gate_intent(**(base | {"review": INTENT["review"] | {"rows": 4}}))
    assert changed["id"] == INTENT["id"] and changed["binding_sha256"] != INTENT["binding_sha256"]
    with pytest.raises(PipelineGateError):
        await store.open(changed)
    journal = copy.deepcopy(F["journal"])
    journal[2]["output"]["state"] = "rejected"
    reply = await invoke(pipeline(store, ignored=True), journal)
    assert reply["result"]["status"] == "failed"
    assert reply["result"]["error"] == "pipeline-gate:blocked"


@pytest.mark.asyncio
async def test_external_input_and_explicit_cancellation():
    store, transport = Store(), Transport()
    base = {k: v for k, v in INTENT.items() if k not in ("version", "id", "review_sha256", "binding_sha256")}
    intent = pipeline_gate_intent(**(base | {"kind": "input"}))
    await store.open(intent)
    s = service(store, transport)
    with pytest.raises(PipelineGateError):
        await decide(s, intent=intent)
    with pytest.raises(PipelineGateError):
        await decide(s, "submitted", intent=intent, payload={})
    assert (await decide(s, "submitted", intent=intent)).decision.state == "submitted"
    store = Store()
    await store.open(INTENT)
    assert (await decide(service(store, transport), "cancelled")).decision.state == "cancelled"


@pytest.mark.asyncio
async def test_deadline_and_task_cancellation_before_commit():
    store, transport = Store(), Transport()
    await store.open(INTENT)
    entered = asyncio.Event()

    async def hanging(*_):
        entered.set()
        await asyncio.Event().wait()

    s = service(store, transport, hanging)
    with pytest.raises(Exception):
        await s.decide(
            INTENT["id"],
            binding_sha256=INTENT["binding_sha256"],
            state="approved",
            auth="alice",
            payload={"reason": "checked"},
            timeout=0.01,
        )
    task = asyncio.create_task(decide(s))
    await entered.wait()
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task
    assert store.rows[INTENT["id"]].decision is None


@pytest.mark.asyncio
async def test_native_scope_and_root_input_verification_and_stable_publish_key():
    calls, owner = [], "alice"

    def handler(request):
        path = request.url.path
        calls.append((path, request.headers.get("Idempotency-Key"), request.content))
        value = (
            {"id": SCOPE.account_id}
            if path == "/v1/account"
            else {"id": SCOPE.app_id}
            if path.endswith("/apps/imports")
            else {"steps": []}
            if path.endswith("/steps")
            else {"status": "received", "event_name": "pipeline-gate." + INTENT["id"]}
            if path.endswith("/events")
            else dict(
                id=INTENT["workflow_run_id"],
                app_id=SCOPE.app_id,
                workflow_name=PIPELINE.spec["name"],
                status="awaiting_event",
                input=F["envelope"] | {"owner_id": owner},
            )
        )
        return httpx.Response(200, json=value)

    transport = NativePipelineGateTransport(
        scope=SCOPE,
        token="backend",
        contract=PIPELINE,
        timeout=1,
        httpx_args={"transport": httpx.MockTransport(handler)},
    )
    assert await transport.active(INTENT)
    await transport.publish(INTENT)
    await transport.publish(INTENT)
    assert calls[-1] == calls[-2] and calls[-1][1] == "pipeline-gate:" + INTENT["id"]
    owner = "bob"
    with pytest.raises(Exception):
        await transport.active(INTENT)


def test_live_waiting_metadata_and_detached_record():
    value = dict(
        version=1,
        resource_id="review",
        run_id=INTENT["workflow_run_id"],
        status="running",
        total=1,
        succeeded=1,
        failed=0,
        cancelled=0,
        running=0,
        report="none",
        failures=[],
        waiting=dict(kind="approval", gate_id=INTENT["id"], name="review"),
    )
    assert LiveJobSnapshot.from_dict(value, "review").to_dict() == value
    with pytest.raises(Exception):
        LiveJobSnapshot.from_dict(value | {"waiting": value["waiting"] | {"gate_id": "unsafe"}}, "review")
    r = PipelineGateRecord.from_dict(F["record"])
    r.intent["review"]["rows"] = 100
    assert job_value_hash(r.intent) == job_value_hash(INTENT)


@pytest.mark.asyncio
async def test_bounded_wakes_and_expired_cancelled_journal_outcomes():
    store, journal = Store(), []
    for _ in range(40):
        reply = await invoke(pipeline(store), journal)
        if reply["result"]["status"] == "failed":
            break
        commit(journal, reply)
    assert reply["result"]["status"] == "failed"
    assert reply["result"]["error"] == "pipeline-gate:wake_limit"
    for choice in ("expired", "cancelled"):
        saved = copy.deepcopy(F["journal"])
        saved[2]["output"] = dict(
            state=choice, actor_id=None, payload=None, decided_at=F["record"]["decision"]["decided_at"]
        )
        result = await invoke(pipeline(store), saved)
        assert result["result"]["status"] == "succeeded"
        assert result["result"]["output"]["output"] == {"n": 0}


@pytest.mark.asyncio
async def test_external_input_journal_replay_preserves_typed_payload():
    base = {k: v for k, v in INTENT.items() if k not in ("version", "id", "review_sha256", "binding_sha256")}
    intent = pipeline_gate_intent(**(base | {"kind": "input"}))
    journal = copy.deepcopy(F["journal"])
    journal[0]["output"]["binding_sha256"] = intent["binding_sha256"]
    journal[2]["output"]["state"] = "submitted"

    async def run(_value, wf):
        outcome = await wf.wait_for_input(
            "review", CONTRACT, INTENT["review"], options=PipelineGateOptions(timeout=60, required_roles=("reviewer",))
        )
        assert outcome.state == "submitted" and outcome.payload == {"reason": "checked"}
        return {"n": len(outcome.payload["reason"])}

    p = JobPipeline(PIPELINE, scope=SCOPE, store=None, gate_store=Store(), jobs={}, run=run)
    result = await invoke(p, journal)
    assert result["result"]["status"] == "succeeded"
    assert result["result"]["output"]["output"] == {"n": 7}


@pytest.mark.asyncio
async def test_shared_postgres_statements_including_rotating_recovery_batches():
    queries = []

    async def query(sql, values):
        queries.append(sql)
        return [{"record": F["record"]}]

    store = PostgresPipelineGateStore(query)
    await store.open(INTENT)
    await store.resolve(INTENT["id"])
    await store.decide(
        INTENT["id"], INTENT["binding_sha256"], dict(state="approved", actor_id="alice", payload={"reason": "checked"})
    )
    await store.mark(INTENT["id"], PipelineGateRecord.from_dict(F["record"]).decision, "delivered")
    await store.pending(2, SCOPE, PIPELINE.sha256, CONTRACT.sha256)
    await store.list(INTENT["workflow_run_id"], "alice")
    assert queries == F["queries"]
