import asyncio
import copy

import pytest
from outbound_action_fixture import F, Fixture

from faas_sdk.preview.outbound_action_mcp import OutboundActionMCP
from faas_sdk.preview.outbound_action_state import OutboundActionError
from faas_sdk.preview.outbound_actions import OutboundActionResponse
from faas_sdk.preview.pipeline_tool_mcp import PipelineToolMCPError
from faas_sdk.preview.stripe_refund_actions import stripe_refund_contract


async def approved():
    f = Fixture()
    p = await f.action.prepare(F["input"], F["request_id"], "alice", timeout=1)
    ctx = await f.start(p)
    await f.until_gate(ctx)
    await f.approve(ctx)
    return f, p, ctx


@pytest.mark.parametrize("padding", [70000, 1048576])
async def test_provider_responses_allow_full_objects_while_remaining_bounded(padding):
    f, p, ctx = await approved()
    f.account_padding = padding
    if padding < 1048576:
        assert (await f.action.execute(p["action_id"], ctx["run_id"], timeout=1))["outcome"]["state"] == "succeeded"
        assert f.posts == 1
    else:
        with pytest.raises(OutboundActionError, match="limit"):
            await f.action.execute(p["action_id"], ctx["run_id"], timeout=1)
        assert f.posts == 0
        assert (await f.action.inspect(p["action_id"], "alice", timeout=1))["outcome"]["state"] == "prepared"


async def test_review_stop_retains_known_provider_reference():
    f, p, ctx = await approved()
    f.pending = True
    sent = await f.action.execute(p["action_id"], ctx["run_id"], timeout=1)
    f.refunds[0]["amount"] = 999
    stopped = await f.action.reconcile(p["action_id"], "alice", timeout=1)
    assert stopped["outcome"]["state"] == "needs_review"
    assert stopped["outcome"]["reference"] == sent["outcome"]["reference"]
    assert f.posts == 1


async def test_shared_intent_contracts_and_mcp():
    f = Fixture()
    p = await f.action.prepare(F["input"], F["request_id"], "alice", timeout=1)
    assert f.store.rows[p["action_id"]]["intent"] == F["intent"]
    assert f.action.contract.spec == F["root_contract"]
    assert f.action.review_contract.spec == F["review_contract"]
    assert stripe_refund_contract.spec == F["business_contract"]
    assert OutboundActionMCP(f.action).tools == F["tools"]


async def test_concurrent_prepares_and_configuration_conflicts():
    f = Fixture()
    values = await asyncio.gather(
        *(f.action.prepare(F["input"], F["request_id"], "alice", timeout=1) for _ in range(10))
    )
    assert all(v == values[0] for v in values) and len(f.store.rows) == 1
    with pytest.raises(OutboundActionError, match="conflict"):
        await f.action.prepare(F["input"] | dict(currency="eur"), F["request_id"], "alice", timeout=1)
    with pytest.raises(OutboundActionError, match="conflict"):
        await f.make("v2").inspect(values[0]["action_id"], "alice", timeout=1)


@pytest.mark.parametrize("choice", ["rejected", "expired"])
async def test_approval_required(choice):
    f = Fixture()
    p = await f.action.prepare(F["input"], F["request_id"], "alice", timeout=1)
    ctx = await f.start(p)
    await f.until_gate(ctx)
    with pytest.raises(OutboundActionError, match="approval_required"):
        await f.action.execute(p["action_id"], ctx["run_id"], timeout=1)
    await f.approve(ctx, choice)
    assert (await f.finish(ctx))["decision"] == choice
    assert f.posts == 0


async def test_concurrent_dispatch_has_one_acknowledged_claim():
    f, p, ctx = await approved()
    values = await asyncio.gather(*(f.action.execute(p["action_id"], ctx["run_id"], timeout=1) for _ in range(8)))
    assert f.posts == 1 and any(v["outcome"]["state"] == "succeeded" for v in values)


async def test_lost_provider_response_reconciles_without_post():
    f, p, ctx = await approved()
    f.lose_response = True
    assert (await f.action.execute(p["action_id"], ctx["run_id"], timeout=1))["outcome"]["state"] == "unknown"
    assert (await f.action.execute(p["action_id"], ctx["run_id"], timeout=1))["outcome"]["state"] == "unknown"
    result = await f.action.reconcile(p["action_id"], "alice", timeout=1)
    assert result["outcome"]["state"] == "succeeded" and result["outcome"]["result"]["amount_minor"] == 1000
    assert f.posts == 1


async def test_pending_reference_is_retained():
    f, p, ctx = await approved()
    f.pending = True
    assert (await f.action.execute(p["action_id"], ctx["run_id"], timeout=1))["outcome"]["state"] == "pending"
    f.refunds[0]["status"] = "succeeded"
    assert (await f.action.reconcile(p["action_id"], "alice", timeout=1))["outcome"]["state"] == "succeeded"
    assert f.posts == 1


@pytest.mark.parametrize("duplicate", [False, True])
async def test_missing_or_duplicate_evidence_does_not_permit_retry(duplicate):
    f, p, ctx = await approved()
    f.lose_response = True
    await f.action.execute(p["action_id"], ctx["run_id"], timeout=1)
    f.missing, f.duplicate = not duplicate, duplicate
    assert (await f.action.reconcile(p["action_id"], "alice", timeout=1))["outcome"]["state"] == (
        "needs_review" if duplicate else "unknown"
    )
    await f.action.execute(p["action_id"], ctx["run_id"], timeout=1)
    assert f.posts == 1


async def test_current_authorization_and_approval_binding():
    f, p, ctx = await approved()
    with pytest.raises(OutboundActionError, match="unauthorized"):
        await f.action.inspect(p["action_id"], "bob", timeout=1)
    f.revoked = True
    for operation in (f.action.inspect, f.action.reconcile):
        with pytest.raises(OutboundActionError, match="unauthorized"):
            await operation(p["action_id"], "alice", timeout=1)
    with pytest.raises(OutboundActionError, match="unauthorized"):
        await f.action.execute(p["action_id"], ctx["run_id"], timeout=1)
    assert f.posts == 0


async def test_recheck_after_provider_account_read():
    f, p, ctx = await approved()
    f.on_verify = lambda: setattr(f, "execution_allowed", False)
    with pytest.raises(OutboundActionError, match="unauthorized"):
        await f.action.execute(p["action_id"], ctx["run_id"], timeout=1)
    assert f.posts == 0


async def test_wrong_account_and_native_binding_fail():
    f, p, ctx = await approved()
    f.bad_account = True
    with pytest.raises(OutboundActionError, match="conflict"):
        await f.action.execute(p["action_id"], ctx["run_id"], timeout=1)
    f.bad_account = False
    f.native_runs[ctx["run_id"]]["input"]["input"]["action_id"] = "a" * 64
    with pytest.raises(OutboundActionError):
        await f.action.execute(p["action_id"], ctx["run_id"], timeout=1)
    assert f.posts == 0


@pytest.mark.parametrize("claim", [False, True])
async def test_lost_store_acknowledgements(claim):
    f, p, ctx = await approved()
    f.lose_claim, f.lose_observe = claim, not claim
    if claim:
        with pytest.raises(OutboundActionError, match="store_failed"):
            await f.action.execute(p["action_id"], ctx["run_id"], timeout=1)
    else:
        assert (await f.action.execute(p["action_id"], ctx["run_id"], timeout=1))["outcome"]["state"] == "unknown"
    f.lose_claim = False
    assert (await f.action.execute(p["action_id"], ctx["run_id"], timeout=1))["outcome"]["state"] == (
        "unknown" if claim else "succeeded"
    )
    assert f.posts == (0 if claim else 1)


async def test_discard_and_native_cancel_fence_dispatch():
    f, p, ctx = await approved()
    await f.action.cancel(p["action_id"], "alice", timeout=1)
    assert (await f.action.execute(p["action_id"], ctx["run_id"], timeout=1))["outcome"]["state"] == "cancelled"
    f, p, ctx = await approved()
    f.native_runs[ctx["run_id"]].update(status="failed", cancelled_at="2026-11-17T10:00:00Z")
    with pytest.raises(OutboundActionError, match="execution_inactive"):
        await f.action.execute(p["action_id"], ctx["run_id"], timeout=1)
    assert f.posts == 0


@pytest.mark.parametrize("cancel", [False, True])
async def test_deadline_or_cancellation_after_dispatch_retains_uncertainty(cancel):
    f, p, ctx = await approved()
    f.hang_post = True
    task = None

    def abort():
        if cancel:
            task.cancel()

    f.on_post = abort
    task = asyncio.create_task(f.action.execute(p["action_id"], ctx["run_id"], timeout=2))
    assert (await task)["outcome"]["state"] == "unknown"
    assert (await f.action.reconcile(p["action_id"], "alice", timeout=1))["outcome"]["state"] == "succeeded"
    assert f.posts == 1


async def test_late_claim_after_deadline_cannot_dispatch():
    f, p, ctx = await approved()
    gate = asyncio.Event()

    async def late():
        try:
            await gate.wait()
        except asyncio.CancelledError:
            await gate.wait()

    f.before_claim = late
    with pytest.raises(OutboundActionError, match="deadline"):
        await f.action.execute(p["action_id"], ctx["run_id"], timeout=0.03)
    gate.set()
    await asyncio.sleep(0.01)
    assert f.posts == 0


async def test_mcp_has_no_approval_or_execution_bypass():
    f = Fixture()
    mcp = OutboundActionMCP(f.action)
    assert len(mcp.tools) == 9
    result = await mcp.call(
        "action_csv_refund_prepare", dict(request_id=F["request_id"], input=F["input"]), "alice", timeout=1
    )
    assert result["structuredContent"]["outcome"]["state"] == "prepared" and f.posts == 0
    with pytest.raises(PipelineToolMCPError):
        await mcp.call(
            "action_csv_refund_prepare",
            dict(request_id=F["request_id"], input=F["input"], owner_id="alice"),
            "alice",
            timeout=1,
        )


@pytest.mark.parametrize("case", F["provider_cases"])
async def test_shared_provider_cases(case):
    f = Fixture()
    intent = copy.deepcopy(F["intent"])
    refund = dict(
        id="re_fixture",
        object="refund",
        payment_intent=F["input"]["payment_intent"],
        amount=1000,
        currency="usd",
        status=case["refund_status"],
        metadata=dict(gregale_action_id=intent["identity"], gregale_input_sha256=intent["input_sha256"]),
    )
    outcome = f.action._provider.decode(OutboundActionResponse(case["status"], refund), intent)
    assert outcome["state"] == case["state"]


async def test_workflow_completion_and_fresh_approval_reuse_result():
    f, p, ctx = await approved()
    assert (await f.finish(ctx))["action"]["outcome"]["state"] == "succeeded"
    assert f.posts == 1
    again = await f.start(p)
    await f.until_gate(again)
    await f.approve(again)
    assert (await f.finish(again))["action"]["outcome"]["state"] == "succeeded"
    assert f.posts == 1


async def test_provider_pagination_bounds_and_repeated_cursors():
    from faas_sdk.preview.stripe_refund_actions import StripeRefundActionProvider

    record = dict(intent=F["intent"], version=1, outcome=dict(state="unknown", code="dispatch_unconfirmed"))
    for repeat in (False, True):

        class Reader:
            count = 0

            async def get(self, path):
                self.count += 1
                return OutboundActionResponse(
                    200,
                    dict(
                        object="list",
                        has_more=True,
                        data=[dict(id="re_same" if repeat else "re_" + str(self.count), metadata={})],
                    ),
                )

        reader = Reader()
        provider = StripeRefundActionProvider(account_id="acct_fixture", api_version="2026-10-28.endive", max_pages=2)
        result = await provider.reconcile(reader, record)
        assert result["state"] == ("needs_review" if repeat else "unknown")
        assert result["code"] == ("provider_response_invalid" if repeat else "reconciliation_limit")
        assert reader.count == 2
