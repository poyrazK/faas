import asyncio
import copy
import json
from pathlib import Path
from types import SimpleNamespace
from uuid import uuid4

import httpx
import pytest
from faas_sdk.job_runtime import job_value_hash
from faas_sdk.preview import (
    PIPELINE_CHILDREN_SCHEMA,
    JobPipeline,
    NativePipelineChildrenTransport,
    PipelineCheckpoint,
    PipelineChild,
    PipelineChildOptions,
    PipelineChildren,
    PipelineChildrenError,
    PipelineChildrenReceipt,
    PipelineInspection,
    PipelineScope,
    PostgresPipelineChildrenStore,
    pipeline_children_intent,
    pipeline_children_live_snapshot,
)
from faas_sdk.workflows import WORKFLOW_CODE_PROTOCOL, run_workflow_invocation

from faas_sdk import JobContract, JobInput

ROOT = Path(__file__).resolve().parents[1] / "request-contracts"
if not ROOT.exists():
    ROOT = Path(__file__).resolve().parents[2] / "request-contracts"
F = json.loads((ROOT / "pipeline-children.json").read_text())
SCOPE = PipelineScope.from_dict(F["envelope"]["scope"])
PARENT, CONTRACT = JobContract(F["root_contract"]), JobContract(F["child_contract"])
DEFINITION = PipelineChild(contract=CONTRACT)


class Store:
    def __init__(self):
        self.rows, self.expired, self.lose_save = {}, False, False

    async def open(self, intent):
        old = self.rows.get(intent["identity"])
        if old:
            assert job_value_hash(old.intent) == job_value_hash(intent)
            return old
        r = PipelineChildrenReceipt.from_dict(
            F["receipt"]
            | {
                "intent": intent,
                "state": dict(
                    stop_reason=None,
                    parent_stopped=False,
                    children=[
                        dict(phase="unstarted", checkpoint=None, cancel_requested=False) for _ in intent["inputs"]
                    ],
                ),
            }
        )
        self.rows[intent["identity"]] = r
        return r

    async def load(self, identity):
        r = self.rows.get(identity)
        return PipelineChildrenReceipt.from_dict(r.to_dict() | {"expired": self.expired}) if r else None

    async def save(self, old, state, admission):
        if self.rows[old.intent["identity"]].revision != old.revision or admission and self.expired:
            return None
        r = PipelineChildrenReceipt.from_dict(
            old.to_dict() | dict(state=state, revision=old.revision + 1, expired=self.expired)
        )
        self.rows[old.intent["identity"]] = r
        if self.lose_save:
            self.lose_save = False
            raise RuntimeError("lost store ACK")
        return r

    async def scan(self, scope, parent, limit):
        return [
            r
            for r in self.rows.values()
            if r.intent["scope"] == scope.to_dict() and r.intent["parent_sha256"] == parent
        ][:limit]


class Native:
    def __init__(self):
        self.posts = self.peak = 0
        self.held = self.fail_first = self.lose_start = self.lose_cancel = self.parent_stopped = self.awaiting = False
        self.runs = {}

    async def parent(self, i):
        return PipelineInspection(
            run_id=i["parent_run_id"],
            status="failed" if self.parent_stopped else "running",
            cancelled=self.parent_stopped,
            steps=(),
        )

    async def start(self, i, n):
        self.posts += 1
        c = PipelineCheckpoint(
            version=1,
            run_id=str(uuid4()),
            scope=SCOPE,
            owner_id=i["owner_id"],
            contract_sha256=i["child_sha256"],
            input_sha256=i["inputs"][n]["input_sha256"],
        )
        self.runs[c.run_id] = dict(n=n, checkpoint=c, done=False, cancelled=False)
        self.peak = max(self.peak, sum(not r["done"] for r in self.runs.values()))
        if self.lose_start:
            raise RuntimeError("lost native ACK")
        return c

    async def attach(self, i, n, run_id):
        return self.runs[run_id]["checkpoint"]

    async def observe(self, i, n, c):
        r = self.runs[c.run_id]
        value = dict(checkpoint=c.to_dict(), cancel_requested=False)
        if r["cancelled"]:
            return value | dict(phase="cancelled", native_status="failed")
        if self.held:
            return value | dict(phase="running", native_status="awaiting_event" if self.awaiting else "running")
        r["done"] = True
        return value | (
            dict(phase="failed", native_status="failed")
            if self.fail_first and n == 0
            else dict(phase="succeeded", native_status="succeeded", output={"n": i["inputs"][n]["value"]["n"] + 1})
        )

    async def cancel(self, i, n, c):
        self.runs[c.run_id]["cancelled"] = self.runs[c.run_id]["done"] = True
        if self.lose_cancel:
            raise RuntimeError("lost cancel ACK")


def scene(**policy):
    s = SimpleNamespace(store=Store(), native=Native())

    def coordinator():
        return PipelineChildren(
            scope=SCOPE, parent_contract=PARENT, children=[DEFINITION], store=s.store, transport=s.native
        )

    async def run(value, wf):
        result = await wf.children(
            "imports",
            DEFINITION,
            [JobInput(i["id"], i["value"]) for i in F["receipt"]["intent"]["inputs"]],
            options=PipelineChildOptions(timeout=60, concurrency=2, **policy),
        )
        return {"n": sum(c.output["n"] for c in result.children if c.phase == "succeeded")}

    async def invoke(journal=None):
        p = JobPipeline(PARENT, scope=SCOPE, store=None, jobs={}, children=coordinator(), run=run)
        return await run_workflow_invocation(
            p.definition,
            dict(
                protocol=WORKFLOW_CODE_PROTOCOL,
                run_id=F["receipt"]["intent"]["parent_run_id"],
                workflow=PARENT.spec["name"],
                input=F["envelope"],
                invocation=len(journal or []) + 1,
                journal=journal or [],
            ),
        )

    s.coordinator, s.invoke = coordinator, invoke
    return s


def commit(journal, reply):
    for op in reply["ops"]:
        entry = op | {"status": "succeeded" if op["kind"] == "sleep" else op["status"], "attempt": 1}
        if op["seq"] < len(journal):
            journal[op["seq"]] = entry
        else:
            assert op["seq"] == len(journal)
            journal.append(entry)


@pytest.mark.asyncio
async def test_shared_intent_schema_and_node_completed_journal():
    base = {k: v for k, v in F["receipt"]["intent"].items() if k not in ("version", "identity")}
    assert pipeline_children_intent(**base) == F["receipt"]["intent"]
    assert PipelineChildrenReceipt.from_dict(F["final"]).to_dict() == F["final"]
    assert PIPELINE_CHILDREN_SCHEMA == (ROOT / "pipeline-children.sql").read_text()
    s = scene()
    reply = await s.invoke(F["journal"])
    assert reply["result"]["status"] == "succeeded"
    assert reply["result"]["output"]["output"] == {"n": 9}
    assert s.native.posts == 0


@pytest.mark.asyncio
async def test_dropped_journals_fresh_coordinators_ordered_results():
    s, journal, dropped, output = scene(), [], set(), None
    for _ in range(30):
        reply = await s.invoke(journal)
        if reply["result"]["status"] == "succeeded":
            output = reply["result"]["output"]["output"]
            break
        assert all(op.get("status") != "failed" for op in reply["ops"]), reply
        seq = reply["ops"][0]["seq"]
        if seq not in dropped:
            dropped.add(seq)
            continue
        commit(journal, reply)
    assert output == {"n": 9} and s.native.posts == 3 and s.native.peak <= 2
    view = await s.coordinator().inspect(F["result"]["parent_run_id"], "imports", "alice", DEFINITION, timeout=1)
    assert [c.output for c in view.children] == [{"n": 2}, {"n": 3}, {"n": 4}]


@pytest.mark.asyncio
async def test_concurrent_drivers_enforce_shared_admission_cap():
    s, journal = scene(), []
    s.native.held = True
    commit(journal, await s.invoke())
    replies = await asyncio.gather(s.invoke(journal), s.invoke(journal))
    assert all(all(o.get("status") != "failed" for o in r["ops"]) for r in replies)
    assert s.native.posts == s.native.peak == 2
    await s.invoke(journal)
    assert s.native.posts == 2


@pytest.mark.asyncio
async def test_unknown_start_reconcile_and_owner_isolation():
    s, journal = scene(), []
    s.native.held = s.native.lose_start = True
    commit(journal, await s.invoke())
    commit(journal, await s.invoke(journal))
    await s.invoke(journal)
    assert s.native.posts == 2
    rows = list(s.native.runs.values())
    with pytest.raises(PipelineChildrenError, match="conflict"):
        await s.coordinator().reconcile(
            F["result"]["parent_run_id"], "imports", "item-1", rows[1]["checkpoint"].run_id, "alice", timeout=1
        )
    await s.coordinator().reconcile(
        F["result"]["parent_run_id"], "imports", "item-1", rows[0]["checkpoint"].run_id, "alice", timeout=1
    )
    with pytest.raises(PipelineChildrenError, match="forbidden"):
        await s.coordinator().inspect(F["result"]["parent_run_id"], "imports", "bob", DEFINITION, timeout=1)
    other, j = scene(), []
    commit(j, await other.invoke())
    other.store.lose_save = True
    assert (await other.invoke(j))["ops"][0]["status"] == "failed"
    assert other.native.posts == 0
    await other.invoke(j)
    assert other.native.posts == 1


@pytest.mark.asyncio
async def test_fail_fast_stops_admission_and_recovers_uncertain_cancellation():
    s, j = scene(failure_policy="fail_fast", on_fail_fast="cancel_children"), []
    s.native.held = True
    commit(j, await s.invoke())
    commit(j, await s.invoke(j))
    commit(j, await s.invoke(j))
    s.native.held, s.native.fail_first, s.native.lose_cancel = False, True, True
    original = s.native.observe

    async def observe(i, n, c):
        return (
            dict(phase="running", checkpoint=c.to_dict(), cancel_requested=False, native_status="running")
            if n == 1
            else await original(i, n, c)
        )

    s.native.observe = observe
    assert (await s.invoke(j))["ops"][0]["status"] == "failed"
    s.native.lose_cancel, s.native.observe = False, original
    await s.coordinator().recover(10, timeout=1)
    view = await s.coordinator().inspect(F["result"]["parent_run_id"], "imports", "alice", DEFINITION, timeout=1)
    assert view.stop_reason == "child_failed" and [c.phase for c in view.children] == ["failed", "cancelled", "skipped"]
    assert s.native.posts == 2


@pytest.mark.asyncio
async def test_deadline_and_parent_stop_policies():
    for reason in ("deadline", "parent"):
        s, j = scene(on_deadline="cancel_children", on_parent_stopped="cancel_children"), []
        s.native.held = True
        commit(j, await s.invoke())
        commit(j, await s.invoke(j))
        if reason == "deadline":
            s.store.expired = True
        else:
            s.native.parent_stopped = True
        await s.coordinator().recover(10, timeout=1)
        await s.coordinator().recover(10, timeout=1)
        view = await s.coordinator().inspect(F["result"]["parent_run_id"], "imports", "alice", DEFINITION, timeout=1)
        assert [c.phase for c in view.children] == ["cancelled", "cancelled", "skipped"] and s.native.posts == 2


def test_input_policy_checkpoint_and_size_bounds():
    base = {k: v for k, v in F["receipt"]["intent"].items() if k not in ("version", "identity")}
    for patch in (
        dict(concurrency=0),
        dict(concurrency=11),
        dict(timeout_ms=0),
        dict(inputs=[]),
        dict(inputs=[base["inputs"][0]] * 2),
    ):
        with pytest.raises(PipelineChildrenError):
            pipeline_children_intent(**(base | patch))
    malformed = copy.deepcopy(F["final"])
    malformed["state"]["children"][0]["checkpoint"]["owner_id"] = "bob"
    with pytest.raises(PipelineChildrenError):
        PipelineChildrenReceipt.from_dict(malformed)


@pytest.mark.asyncio
async def test_native_parent_and_child_provenance_and_typed_result():
    owner, account = "alice", SCOPE.account_id
    lineage = copy.deepcopy(F["input_envelopes"][0]["lineage"])
    c = F["final"]["state"]["children"][0]["checkpoint"]

    def handler(req):
        p = req.url.path
        child = c["run_id"] in p
        value = (
            dict(id=account)
            if p == "/v1/account"
            else dict(id=SCOPE.app_id)
            if p.endswith("/apps/imports")
            else dict(steps=[])
            if p.endswith("/steps")
            else dict(
                id=c["run_id"] if child else F["result"]["parent_run_id"],
                app_id=SCOPE.app_id,
                workflow_name=CONTRACT.spec["name"] if child else PARENT.spec["name"],
                status="succeeded" if child else "running",
                input=(F["input_envelopes"][0] | {"owner_id": owner, "lineage": lineage}) if child else F["envelope"],
                output=dict(
                    version=1, scope=SCOPE.to_dict(), owner_id=owner, contract_sha256=CONTRACT.sha256, output={"n": 2}
                ),
            )
        )
        return httpx.Response(200, json=value)

    t = NativePipelineChildrenTransport(
        scope=SCOPE,
        parent_contract=PARENT,
        children=[DEFINITION],
        token="backend",
        httpx_args={"transport": httpx.MockTransport(handler)},
    )
    assert (await t.parent(F["receipt"]["intent"])).status == "running"
    assert (await t.observe(F["receipt"]["intent"], 0, PipelineCheckpoint.from_dict(c)))["phase"] == "succeeded"
    owner = "bob"
    with pytest.raises(Exception):
        await t.observe(F["receipt"]["intent"], 0, PipelineCheckpoint.from_dict(c))
    owner = "alice"
    lineage = lineage | {"item_id": "unrelated-item"}
    with pytest.raises(Exception):
        await t.observe(F["receipt"]["intent"], 0, PipelineCheckpoint.from_dict(c))
    account = str(uuid4())
    with pytest.raises(Exception):
        await t.parent(F["receipt"]["intent"])


@pytest.mark.asyncio
async def test_live_parent_view_includes_pending_child_review():
    s, j = scene(), []
    s.native.held = s.native.awaiting = True
    commit(j, await s.invoke())
    commit(j, await s.invoke(j))

    class Gates:
        async def list(self, run_id, owner):
            return [
                SimpleNamespace(
                    decision=None,
                    intent=dict(
                        pipeline_sha256=CONTRACT.sha256,
                        owner_id="alice",
                        scope=SCOPE.to_dict(),
                        kind="approval",
                        id="a" * 64,
                        name="review",
                    ),
                )
            ]

    co = PipelineChildren(
        scope=SCOPE,
        parent_contract=PARENT,
        children=[DEFINITION],
        store=s.store,
        transport=s.native,
        gate_store=Gates(),
    )
    view = await co.inspect(F["result"]["parent_run_id"], "imports", "alice", DEFINITION, timeout=1)
    assert view.children[0].waiting.kind == "approval"
    p = PipelineInspection(run_id=view.parent_run_id, status="running", cancelled=False, steps=())
    assert pipeline_children_live_snapshot(view, p, "batch").waiting.gate_id == "a" * 64


@pytest.mark.asyncio
async def test_task_cancellation_before_reservation():
    s, entered = scene(), asyncio.Event()

    async def parent(i):
        entered.set()
        await asyncio.Event().wait()

    s.native.parent = parent
    task = asyncio.create_task(s.invoke())
    await entered.wait()
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task
    assert s.native.posts == 0


@pytest.mark.asyncio
async def test_shared_postgres_cas_and_rotating_recovery_queries():
    queries = []

    async def query(sql, values):
        queries.append(sql)
        return [{"receipt": F["receipt"]}]

    store = PostgresPipelineChildrenStore(query)
    await store.open(F["receipt"]["intent"])
    await store.load(F["receipt"]["intent"]["identity"])
    await store.save(PipelineChildrenReceipt.from_dict(F["receipt"]), F["receipt"]["state"], True)
    await store.scan(SCOPE, PARENT.sha256, 2)
    assert queries == F["queries"]


@pytest.mark.asyncio
async def test_expiry_after_reservation_ack_before_post_retires_known_unsent_child():
    s, journal = scene(), []
    commit(journal, await s.invoke())
    save = s.store.save

    async def expiring(old, state, admission):
        result = await save(old, state, admission)
        if admission and result:
            s.store.expired = True
        return result

    s.store.save = expiring
    reply = await s.invoke(journal)
    assert reply["ops"][0]["status"] == "succeeded"
    assert s.native.posts == 0
    view = await s.coordinator().inspect(F["result"]["parent_run_id"], "imports", "alice", DEFINITION, timeout=1)
    assert view.stop_reason == "deadline" and all(c.phase == "skipped" for c in view.children)


@pytest.mark.asyncio
async def test_single_child_and_compact_intermediate_batch_journal():
    s, journal = scene(), []

    async def run(_value, wf):
        child = await wf.child("onechild", DEFINITION, {"n": 1}, options=PipelineChildOptions(timeout=60))
        return {"n": child.output["n"]}

    p = JobPipeline(PARENT, scope=SCOPE, store=None, jobs={}, children=s.coordinator(), run=run)
    output = None
    for n in range(8):
        r = await run_workflow_invocation(
            p.definition,
            dict(
                protocol=WORKFLOW_CODE_PROTOCOL,
                run_id=F["result"]["parent_run_id"],
                workflow=PARENT.spec["name"],
                input=F["envelope"],
                invocation=n + 1,
                journal=journal,
            ),
        )
        if r["result"]["status"] == "succeeded":
            output = r["result"]["output"]["output"]
            break
        commit(journal, r)
    assert output == {"n": 2} and s.native.posts == 1
    held, journal = scene(), []
    held.native.held = True
    commit(journal, await held.invoke())
    r = await held.invoke(journal)
    assert "children" not in r["ops"][0]["output"]
    view = await held.coordinator().inspect(F["result"]["parent_run_id"], "imports", "alice", DEFINITION, timeout=1)
    assert len(view.children) == 3
