import asyncio
import json
from datetime import timedelta

import pytest

from faas_sdk.workflows import (
    WORKFLOW_CODE_PROTOCOL,
    WORKFLOW_RESPONSE_MAX_BYTES,
    NonRetryableError,
    RetryPolicy,
    WorkflowStepError,
    parse_duration_ms,
    run_workflow_invocation,
    run_workflow_locally,
    serve_workflows,
    workflow,
)


def request(name, journal, payload=None):
    return {"protocol": WORKFLOW_CODE_PROTOCOL, "run_id": "run-1", "workflow": name, "input": payload or {}, "invocation": len(journal) + 1, "journal": journal}


@workflow("order")
async def order(order_input, wf):
    charge = await wf.step("charge", lambda ctx: {"id": f"ch_{order_input['id']}", "key": ctx.idempotency_key})
    await wf.sleep("cool_off", "3d")
    approval = await wf.wait_for_event("approval", event="approved", timeout="7d")
    if approval is None:
        return {"status": "expired", "charge": charge["id"]}
    return {"status": "shipped" if approval["ok"] else "refused", "charge": charge["id"]}


@pytest.mark.asyncio
async def test_first_call_runs_first_step_and_suspends():
    response = await run_workflow_invocation([order], request("order", [], {"id": "42"}))
    assert response["protocol"] == WORKFLOW_CODE_PROTOCOL
    assert response["result"] == {"status": "running"}
    assert response["ops"] == [{"seq": 0, "name": "charge", "kind": "step", "status": "succeeded", "output": {"id": "ch_42", "key": "workflow/run-1/charge"}}]


@pytest.mark.asyncio
async def test_recorded_steps_replay_and_waits_report_once():
    calls = []

    @workflow("counted")
    async def counted(_input, wf):
        await wf.step("charge", lambda ctx: calls.append(ctx.attempt) or 1)
        await wf.sleep("cool_off", 5)
        return "done"

    journal = [{"seq": 0, "name": "charge", "kind": "step", "status": "succeeded", "attempt": 1, "output": 1}]
    response = await run_workflow_invocation(counted, request("counted", journal))
    assert calls == []
    assert response["ops"] == [{"seq": 1, "name": "cool_off", "kind": "sleep", "duration_ms": 5000}]
    waiting = await run_workflow_invocation(counted, request("counted", journal + [{"seq": 1, "name": "cool_off", "kind": "sleep", "status": "waiting"}]))
    assert waiting["ops"] == [] and waiting["result"]["status"] == "running"
    done = await run_workflow_invocation(counted, request("counted", journal + [{"seq": 1, "name": "cool_off", "kind": "sleep", "status": "succeeded"}]))
    assert done["result"] == {"status": "succeeded", "output": "done"}


@pytest.mark.asyncio
async def test_parallel_steps_run_in_one_call_in_sequence_order():
    async def slow(_ctx):
        await asyncio.sleep(0.02)
        return "a"

    @workflow("fanout")
    async def fanout(_input, wf):
        a, b = await asyncio.gather(wf.step("slow", slow), wf.step("fast", lambda _ctx: "b"))
        return a + b

    first = await run_workflow_invocation(fanout, request("fanout", []))
    assert [(op["seq"], op["name"], op["output"]) for op in first["ops"]] == [(0, "slow", "a"), (1, "fast", "b")]
    journal = [{"seq": op["seq"], "name": op["name"], "kind": "step", "status": "succeeded", "attempt": 1, "output": op["output"]} for op in first["ops"]]
    assert (await run_workflow_invocation(fanout, request("fanout", journal)))["result"] == {"status": "succeeded", "output": "ab"}


@pytest.mark.asyncio
async def test_repeated_names_get_stable_suffixes():
    @workflow("loop")
    async def loop(_input, wf):
        for i in range(3):
            await wf.step("poll", lambda _ctx, i=i: i)
        return "ok"

    result = await run_workflow_locally(loop)
    assert result.status == "succeeded"
    assert [entry["name"] for entry in result.journal] == ["poll", "poll#2", "poll#3"]


@pytest.mark.asyncio
async def test_changed_code_is_reported_for_the_engine_to_refuse():
    @workflow("order")
    async def changed(_input, wf):
        await wf.step("charge_v2", lambda _ctx: 1)

    response = await run_workflow_invocation(changed, request("order", [{"seq": 0, "name": "charge", "kind": "step", "status": "succeeded", "attempt": 1, "output": 1}]))
    assert response["ops"] == [{"seq": 0, "name": "charge_v2", "kind": "step", "status": "succeeded"}]


@pytest.mark.asyncio
async def test_failures_retry_then_raise_into_the_workflow():
    def boom(ctx):
        raise RuntimeError(f"boom {ctx.attempt}")

    @workflow("flaky")
    async def flaky(_input, wf):
        try:
            await wf.step("call", boom, retry=RetryPolicy(max_attempts=2, backoff="exponential"))
        except WorkflowStepError as error:
            return {"recovered": str(error), "attempts": error.attempts}
        return "unreachable"

    first = await run_workflow_invocation(flaky, request("flaky", []))
    assert first["ops"] == [{"seq": 0, "name": "call", "kind": "step", "retry": {"max_attempts": 2, "backoff": "exponential"}, "status": "failed", "error": "boom 1"}]
    local = await run_workflow_locally(flaky)
    assert local.output == {"recovered": "boom 2", "attempts": 2}


@pytest.mark.asyncio
async def test_scheduled_retry_stays_suspended_while_the_rest_continues():
    runs = []

    async def branch(wf):
        await wf.sleep("pause", 1)
        return await wf.step("after", lambda _ctx: 2)

    @workflow("both")
    async def both(_input, wf):
        await asyncio.gather(wf.step("call", lambda _ctx: runs.append(1) or 1), branch(wf))
        return "done"

    journal = [
        {"seq": 0, "name": "call", "kind": "step", "status": "scheduled", "attempt": 1, "error": "boom 1"},
        {"seq": 1, "name": "pause", "kind": "sleep", "status": "succeeded"},
    ]
    response = await run_workflow_invocation(both, request("both", journal))
    assert runs == []
    assert response["ops"] == [{"seq": 2, "name": "after", "kind": "step", "status": "succeeded", "output": 2}]
    assert response["result"] == {"status": "running"}


@pytest.mark.asyncio
async def test_operation_names_may_not_use_the_reserved_suffix():
    @workflow("clash")
    async def clash(_input, wf):
        await wf.step("poll", lambda _ctx: 1)
        await wf.step("poll#2", lambda _ctx: 2)

    response = await run_workflow_invocation(clash, request("clash", [{"seq": 0, "name": "poll", "kind": "step", "status": "succeeded", "attempt": 1, "output": 1}]))
    assert response["result"]["status"] == "failed"
    assert "reserved for repeated names" in response["result"]["error"]


@pytest.mark.asyncio
async def test_non_retryable_and_non_json_results():
    def reject(_ctx):
        raise NonRetryableError("card declined")

    @workflow("strict")
    async def strict(_input, wf):
        await wf.step("reject", reject)

    response = await run_workflow_invocation(strict, request("strict", []))
    assert response["ops"][0]["non_retryable"] is True

    @workflow("opaque")
    async def opaque(_input, wf):
        await wf.step("value", lambda _ctx: object())

    failed = await run_workflow_invocation(opaque, request("opaque", []))
    assert failed["ops"][0]["status"] == "failed" and failed["ops"][0]["non_retryable"] is True


@pytest.mark.asyncio
async def test_events_resolve_in_order_and_time_out_to_none():
    approved = await run_workflow_locally(order, {"id": "7"}, events={"approved": [{"ok": True}]})
    assert approved.output == {"status": "shipped", "charge": "ch_7"}
    expired = await run_workflow_locally(order, {"id": "7"})
    assert expired.output == {"status": "expired", "charge": "ch_7"}
    journal = [
        {"seq": 0, "name": "charge", "kind": "step", "status": "succeeded", "attempt": 1, "output": {"id": "ch_7"}},
        {"seq": 1, "name": "cool_off", "kind": "sleep", "status": "succeeded"},
    ]
    response = await run_workflow_invocation(order, request("order", journal))
    assert response["ops"] == [{"seq": 2, "name": "approval", "kind": "event", "event": "approved", "timeout_ms": 7 * 86_400_000}]


@pytest.mark.asyncio
async def test_error_outside_steps_fails_the_workflow():
    @workflow("broken")
    async def broken(_input, _wf):
        raise ValueError("bad input")

    assert (await run_workflow_invocation(broken, request("broken", [])))["result"] == {"status": "failed", "error": "bad input"}


async def call_asgi(app, body, headers=(), method="POST"):
    sent = []
    chunks = [{"type": "http.request", "body": body, "more_body": False}]

    async def receive():
        return chunks.pop(0)

    async def send(message):
        sent.append(message)

    await app({"type": "http", "method": method, "headers": list(headers)}, receive, send)
    return sent[0]["status"], json.loads(sent[1]["body"])


@pytest.mark.asyncio
async def test_asgi_app_only_accepts_gregale_workflow_calls():
    app = serve_workflows([order])
    body = json.dumps(request("order", [], {"id": "1"})).encode()
    status, _ = await call_asgi(app, body)
    assert status == 403
    status, payload = await call_asgi(app, body, [(b"x-faas-workflow-mode", b"code"), (b"x-faas-workflow-run-id", b"run-1")])
    assert status == 200 and len(payload["ops"]) == 1
    status, _ = await call_asgi(serve_workflows([order], allow_unverified_callers=True), json.dumps(request("missing", [])).encode())
    assert status == 404
    status, _ = await call_asgi(app, b"", method="GET")
    assert status == 405


@pytest.mark.asyncio
async def test_results_that_overflow_one_answer_fail_without_retry():
    big = "x" * (600 * 1024)

    @workflow("huge")
    async def huge(_input, wf):
        return await asyncio.gather(wf.step("a", lambda _ctx: big), wf.step("b", lambda _ctx: big))

    response = await run_workflow_invocation(huge, request("huge", []))
    assert response["ops"][0]["status"] == "succeeded"
    assert response["ops"][1]["status"] == "failed" and response["ops"][1]["non_retryable"] is True
    assert "output" not in response["ops"][1]
    assert len(json.dumps(response).encode()) <= WORKFLOW_RESPONSE_MAX_BYTES


def test_parse_duration_ms():
    assert parse_duration_ms(1.5) == 1500
    assert parse_duration_ms("30s") == 30_000
    assert parse_duration_ms("1.5h") == 5_400_000
    assert parse_duration_ms(timedelta(days=3)) == 259_200_000
    with pytest.raises(ValueError):
        parse_duration_ms("soon")
    with pytest.raises(ValueError):
        parse_duration_ms(-1)


def test_workflow_requires_async_function():
    with pytest.raises(TypeError):
        workflow("sync")(lambda _input, _wf: None)
