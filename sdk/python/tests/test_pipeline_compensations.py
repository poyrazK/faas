import asyncio
import json
from uuid import uuid4

import pytest
from compensation_fixture import BUSINESS, CONTRACTS, SOURCE, F, Fixture
from faas_sdk.preview import CompensationError, CompensationMCP, PipelineCompensation
from faas_sdk.preview.pipeline_compensation_state import CompensationPlan, effect_identity

from faas_sdk import JobContext


@pytest.mark.asyncio
async def test_shared_contracts_plan_and_mcp():
    s = Fixture()
    p = await s.recovery.plan(SOURCE["workflow_run_id"], "alice", timeout=1)
    assert p.to_dict() == F["expected_plan"]
    assert {k: v.spec for k, v in CONTRACTS.items()} == F["contracts"]
    assert CompensationMCP(s.recovery).tools == F["tools"]
    assert "backend" not in json.dumps(p.to_dict())
    changed = p.commands[0].input
    changed["row_id"] = "other"
    assert p.commands[0].input["row_id"] == "one"
    with pytest.raises(CompensationError, match="unauthorized"):
        await s.recovery.plan(SOURCE["workflow_run_id"], "bob", timeout=1)


@pytest.mark.asyncio
async def test_concurrent_and_lost_planning_ack():
    s = Fixture()
    plans = await asyncio.gather(*(s.recovery.plan(SOURCE["workflow_run_id"], "alice", timeout=1) for _ in range(16)))
    assert len(s.store.plans) == 1 and len({p.identity for p in plans}) == 1
    lost = Fixture()
    lost.store.lose_reserve = True
    with pytest.raises(CompensationError, match="store_failed"):
        await lost.recovery.plan(SOURCE["workflow_run_id"], "alice", timeout=1)
    lost.store.lose_reserve = False
    assert (await lost.recovery.plan(SOURCE["workflow_run_id"], "alice", timeout=1)).to_dict() == F["expected_plan"]


@pytest.mark.asyncio
async def test_unknown_or_active_sources_block_planning():
    for field, code in (
        ("source_status", "source_active"),
        ("job_status", "source_active"),
        ("unresolved", "source_unresolved"),
    ):
        s = Fixture()
        setattr(s, field, True if field == "unresolved" else "running")
        with pytest.raises(CompensationError, match=code):
            await s.recovery.plan(SOURCE["workflow_run_id"], "alice", timeout=1)
        assert not s.store.plans
    s = Fixture()
    s.job_status = "failed"
    with pytest.raises(CompensationError, match="source_effects_unknown"):
        await s.recovery.plan(SOURCE["workflow_run_id"], "alice", timeout=1)


def task_context(ctx, index=0):
    p = ctx["plan"]
    c = p.commands[index]
    return dict(plan_id=p.identity, command_id=c.id, recovery_run_id=ctx["run_id"]), JobContext(
        input_id=effect_identity(p, c.effect_id), attempt=1, _check=lambda: None
    )


@pytest.mark.asyncio
async def test_approval_order_and_lost_workflow_journals():
    s = Fixture()
    ctx = await s.start()
    await s.until_gate(ctx)
    with pytest.raises(CompensationError, match="approval_required"):
        await s.worker(*task_context(ctx))
    assert s.applies == 0
    await s.approve(ctx)
    dropped = set()
    out = None
    for _ in range(30):
        reply = await s.invoke(ctx)
        if reply["result"]["status"] == "succeeded":
            out = reply["result"]["output"]["output"]
            break
        assert reply["result"]["status"] != "failed", reply
        seq = reply["ops"][0]["seq"]
        if seq not in dropped:
            dropped.add(seq)
            continue
        s.commit(ctx, reply)
    assert out["state"] == "completed" and not s.data and len(s.store.effects) == 2 and s.applies == 2


@pytest.mark.asyncio
async def test_changed_rows_and_review_rollback():
    s = Fixture()
    ctx = await s.start()
    s.data["one"] = dict(version=2)
    await s.until_gate(ctx)
    await s.approve(ctx)
    out = await s.finish(ctx)
    assert (
        out["state"] == "needs_review"
        and s.data == dict(one=dict(version=2), two=dict(version=1))
        and len(s.store.effects) == 1
    )


@pytest.mark.asyncio
async def test_commit_unknown_reuses_atomic_effect():
    s = Fixture()
    ctx = await s.start()
    await s.until_gate(ctx)
    await s.approve(ctx)
    task, context = task_context(ctx)
    s.lose_commit = True
    with pytest.raises(CompensationError, match="commit_unknown"):
        await s.worker(task, context)
    s.lose_commit = False
    assert (await s.worker(task, context))["state"] == "compensated" and s.applies == 1 and len(s.store.effects) == 1


@pytest.mark.asyncio
async def test_callback_and_schema_failures_rollback():
    for field in ("fail_apply", "invalid_output"):
        s = Fixture()
        ctx = await s.start()
        await s.until_gate(ctx)
        await s.approve(ctx)
        setattr(s, field, True)
        with pytest.raises(CompensationError, match="store_failed"):
            await s.worker(*task_context(ctx))
        assert len(s.data) == 2 and not s.store.effects


@pytest.mark.asyncio
async def test_current_authorization_for_plan_start_and_dispatch():
    s = Fixture()
    ctx = await s.start()
    await s.until_gate(ctx)
    s.revoked = True
    with pytest.raises(CompensationError, match="unauthorized"):
        await s.recovery.inspect_plan(ctx["plan"].identity, "alice", timeout=1)
    with pytest.raises(Exception, match="unauthorized"):
        await s.recovery.tool.start(ctx["plan"].input, str(uuid4()), "alice", timeout=1)
    s.revoked = False
    await s.approve(ctx)
    s.execute_allowed = False
    with pytest.raises(CompensationError, match="unauthorized"):
        await s.worker(*task_context(ctx))
    assert not s.store.effects


@pytest.mark.asyncio
async def test_forged_run_and_out_of_order_commands_fail():
    s = Fixture()
    ctx = await s.start()
    await s.until_gate(ctx)
    await s.approve(ctx)
    with pytest.raises(CompensationError, match="conflict"):
        await s.worker(*task_context(ctx, 1))
    task, context = task_context(ctx)
    task["recovery_run_id"] = SOURCE["workflow_run_id"]
    with pytest.raises(CompensationError):
        await s.worker(task, context)
    assert s.applies == 0 and not s.store.effects


@pytest.mark.asyncio
async def test_new_attempt_reuses_completed_effects():
    s = Fixture()
    ctx = await s.start()
    await s.until_gate(ctx)
    await s.approve(ctx)
    await s.finish(ctx)
    other = await s.start()
    await s.until_gate(other)
    await s.approve(other)
    out = await s.finish(other)
    assert (
        out["state"] == "completed"
        and s.applies == 2
        and len(s.store.effects) == 2
        and ctx["run_id"] != other["run_id"]
    )


@pytest.mark.asyncio
async def test_binding_and_configuration_conflicts():
    s = Fixture()
    p = await s.recovery.plan(SOURCE["workflow_run_id"], "alice", timeout=1)
    bad = p.to_dict()
    bad["commands"][0]["input"]["row_id"] = "other"
    with pytest.raises(CompensationError, match="conflict"):
        CompensationPlan.from_dict(bad, BUSINESS)
    changed = PipelineCompensation(BUSINESS, **(s.args | dict(required_roles=("admin",))))
    with pytest.raises(CompensationError, match="conflict"):
        await changed.inspect_plan(p.identity, "alice", timeout=1)


@pytest.mark.asyncio
async def test_mcp_plan_has_no_business_payload_or_approval_tool():
    s = Fixture()
    mcp = CompensationMCP(s.recovery)
    r = await mcp.call(
        "recover_" + F["name"] + "_plan", dict(source_run_id=SOURCE["workflow_run_id"]), "alice", timeout=1
    )
    assert r["structuredContent"]["state"] == "ready" and "expected_version" not in json.dumps(r["structuredContent"])
    with pytest.raises(Exception) as caught:
        await mcp.call("recover_" + F["name"] + "_approve", {}, "alice", timeout=1)
    assert caught.value.code == -32601 and s.applies == 0


@pytest.mark.asyncio
async def test_late_planner_cannot_store_after_deadline():
    gate = asyncio.Event()

    async def late(*args):
        try:
            await asyncio.shield(gate.wait())
        except asyncio.CancelledError:
            await gate.wait()
        from faas_sdk.preview import CompensationCommand

        return [CompensationCommand(**c) for c in F["commands"]]

    s = Fixture(plan=late)
    with pytest.raises(CompensationError, match="deadline"):
        await s.recovery.plan(SOURCE["workflow_run_id"], "alice", timeout=0.02)
    gate.set()
    await asyncio.sleep(0.005)
    assert not s.store.plans


@pytest.mark.asyncio
async def test_cancellation_during_commit_is_unknown_and_receipt_is_reusable():
    s = Fixture()
    ctx = await s.start()
    await s.until_gate(ctx)
    await s.approve(ctx)
    task, context = task_context(ctx)
    started = asyncio.Event()
    s.hang_commit = True
    s.on_commit = started.set
    work = asyncio.create_task(s.worker(task, context))
    await started.wait()
    work.cancel()
    with pytest.raises(CompensationError, match="commit_unknown"):
        await work
    s.hang_commit = False
    s.on_commit = None
    assert (await s.worker(task, context))["state"] == "compensated" and s.applies == 1


@pytest.mark.asyncio
async def test_sql_authorization_is_checked_after_execution_proof():
    s = Fixture()
    ctx = await s.start()
    await s.until_gate(ctx)
    await s.approve(ctx)

    async def deny(*args):
        return False

    async def apply(*args):
        raise AssertionError("must not run")

    worker = s.recovery.handler(pool=s.pool, authorize=deny, apply=apply)
    with pytest.raises(CompensationError, match="unauthorized"):
        await worker(*task_context(ctx))
    assert len(s.data) == 2 and not s.store.effects
