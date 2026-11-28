import asyncio
import json
from contextlib import aclosing
from pathlib import Path

import httpx
import pytest
from faas_sdk.job_runtime import job_value_hash
from faas_sdk.preview import (
    JobContract,
    LiveWorkInboxError,
    PipelineTaskApprovalClient,
    PipelineTaskClarification,
    PipelineTaskClient,
    PipelineTaskClientError,
    PipelineTaskCompletion,
    PipelineTaskHandoffClient,
    PipelineTaskHttpTransport,
    PipelineTaskReviewerInbox,
    work_inbox_invalidation,
)

F = json.loads((Path(__file__).parents[2] / "request-contracts" / "pipeline-task-client-cases.json").read_text())
CONTRACT = JobContract(F["contract"])
CHECKPOINT = {
    "version": 1,
    "tool": F["tool"],
    "contract_sha256": CONTRACT.sha256,
    "request_id": F["request_id"],
    "task_id": F["task_id"],
}
META = "io.modelcontextprotocol/subscriptionId"


def client(http):
    return PipelineTaskClient(
        CONTRACT,
        tool=F["tool"],
        reply_contract=JobContract(F["reply_contract"]),
        transport=PipelineTaskHttpTransport("https://mcp.example.test/mcp", http),
    )


def json_response(result):
    return httpx.Response(200, json={"jsonrpc": "2.0", "id": 1, "result": result})


class Chunks(httpx.AsyncByteStream):
    def __init__(self, messages):
        self.data = "".join(
            "data: " + json.dumps(message, ensure_ascii=False) + "\r\n\r\n" for message in messages
        ).encode()

    async def __aiter__(self):
        split = self.data.index(b"\r\n") + 1
        yield self.data[:split]
        yield self.data[split:]


def sse(messages):
    return httpx.Response(200, stream=Chunks(messages), headers={"content-type": "text/event-stream"})


def ack(ids=None):
    return {
        "jsonrpc": "2.0",
        "method": "notifications/subscriptions/acknowledged",
        "params": {
            "_meta": {META: 1},
            "notifications": {"taskIds": [F["task_id"]] if ids is None else ids},
        },
    }


def notification(wire):
    params = {key: value for key, value in wire.items() if key != "resultType"}
    params["_meta"] = {**wire["_meta"], META: 1}
    return {"jsonrpc": "2.0", "method": "notifications/tasks", "params": params}


@pytest.mark.asyncio
async def test_reconnect_keeps_mutations_explicit():
    calls = []
    streams = 0

    async def handler(request):
        nonlocal streams
        payload = json.loads(request.content)
        method, params = payload["method"], payload["params"]
        calls.append(method)
        assert request.headers["MCP-Method"] == method
        assert "form" in params["_meta"]["io.modelcontextprotocol/clientCapabilities"]["elicitation"]
        if method == "tools/call":
            assert params["arguments"] == {"request_id": F["request_id"], "input": F["input"]}
            return json_response(dict(resultType="task", status="working", taskId=F["task_id"]))
        if method == "subscriptions/listen":
            assert params["notifications"]["taskIds"] == [F["task_id"]]
            streams += 1
            if streams == 1:
                return sse([ack(), notification(F["working"])])
            return sse(
                [
                    ack(),
                    notification(F["working"] | {"lastUpdatedAt": "2026-11-18T09:00:01.000Z"}),
                    notification(F["form"]),
                    notification(F["completed"]),
                ]
            )
        if method == "tasks/update":
            assert params["inputResponses"] == {F["form_key"]: {"action": "accept", "content": F["reply"]}}
            return json_response(dict(resultType="complete"))
        assert method == "resources/read" and params["uri"] == F["resource"]["uri"]
        return json_response({"contents": [F["resource"]]})

    async with httpx.AsyncClient(transport=httpx.MockTransport(handler)) as http:
        sdk = client(http)
        saved = await sdk.start(F["input"], F["request_id"], timeout=1)
        assert saved == CHECKPOINT
        task = sdk.resume(json.loads(json.dumps(saved)))
        states = []
        async with aclosing(task.watch(timeout=1)) as changes:
            async for snapshot in changes:
                states.append(snapshot.status)
                if isinstance(snapshot, PipelineTaskClarification):
                    with pytest.raises(PipelineTaskClientError, match="invalid_input"):
                        await task.answer(snapshot, F["form_key"], {"email_column": "unknown"}, timeout=1)
                    await task.answer(snapshot, F["form_key"], F["reply"], timeout=1)
                if isinstance(snapshot, PipelineTaskCompletion):
                    assert snapshot.output["rows"] == 2
                    assert await task.read_artifact(snapshot, "report", timeout=1) == F["resource"]
        assert states == ["working", "input_required", "completed"]
        assert calls == ["tools/call", "subscriptions/listen", "subscriptions/listen", "tasks/update", "resources/read"]


@pytest.mark.asyncio
async def test_poll_fallback_and_denied_acknowledgement():
    calls = []
    deny = False
    oversized = False

    async def handler(request):
        method = request.headers["MCP-Method"]
        calls.append(method)
        if method == "subscriptions/listen":
            if oversized:
                return httpx.Response(
                    200, content=b"x" * (2 * 1024 * 1024), headers={"content-type": "text/event-stream"}
                )
            if deny:
                return sse([ack([])])
            return httpx.Response(
                200, json={"jsonrpc": "2.0", "id": 1, "error": {"code": -32601, "message": "private"}}
            )
        return json_response(F["working"] if calls.count("tasks/get") == 1 else F["completed"])

    async with httpx.AsyncClient(transport=httpx.MockTransport(handler)) as http:
        task = client(http).resume(CHECKPOINT)
        assert [snapshot.status async for snapshot in task.watch(timeout=1)] == ["working", "completed"]
        assert calls == ["subscriptions/listen", "tasks/get", "tasks/get"]
        deny = True
        with pytest.raises(PipelineTaskClientError, match="not_found"):
            await anext(task.watch(timeout=1))
        assert calls[-1:] == ["subscriptions/listen"]
        oversized = True
        with pytest.raises(PipelineTaskClientError, match="invalid_response"):
            await anext(task.watch(timeout=1))
        with pytest.raises(PipelineTaskClientError, match="invalid_input"):
            client(http).resume(CHECKPOINT | {"tool": "other"})


@pytest.mark.asyncio
@pytest.mark.parametrize("cancel", [False, True])
async def test_deadline_and_cancellation_close_a_blocked_stream(cancel):
    opened = asyncio.Event()
    closed = asyncio.Event()

    class Blocked(httpx.AsyncByteStream):
        async def __aiter__(self):
            opened.set()
            await asyncio.Event().wait()
            yield b""

        async def aclose(self):
            closed.set()

    async def handler(_request):
        return httpx.Response(200, stream=Blocked(), headers={"content-type": "text/event-stream"})

    async with httpx.AsyncClient(transport=httpx.MockTransport(handler)) as http:
        task = client(http).resume(CHECKPOINT)
        async with aclosing(task.watch(timeout=1 if cancel else 0.05)) as iterator:
            pending = asyncio.create_task(anext(iterator))
            await asyncio.wait_for(opened.wait(), timeout=1)
            if cancel:
                pending.cancel()
                with pytest.raises(asyncio.CancelledError):
                    await pending
            else:
                with pytest.raises(PipelineTaskClientError, match="deadline"):
                    await pending
            assert closed.is_set()


@pytest.mark.asyncio
async def test_lost_mutation_response_is_not_retried():
    calls = []

    async def handler(request):
        method = request.headers["MCP-Method"]
        calls.append(method)
        if method == "tasks/get":
            return json_response(F["form"])
        raise httpx.ReadError("lost acknowledgement")

    async with httpx.AsyncClient(transport=httpx.MockTransport(handler)) as http:
        sdk = client(http)
        with pytest.raises(PipelineTaskClientError, match="request_failed"):
            await sdk.start(F["input"], F["request_id"], timeout=1)
        task = sdk.resume(CHECKPOINT)
        form = await task.get(timeout=1)
        assert isinstance(form, PipelineTaskClarification)
        with pytest.raises(PipelineTaskClientError, match="request_failed"):
            await task.answer(form, F["form_key"], F["reply"], timeout=1)
        assert calls == ["tools/call", "tasks/get", "tasks/update"]


@pytest.mark.asyncio
async def test_handoff_uses_compiled_reply_schema_and_view_from_same_grant():
    current = F["form"]["inputRequests"][F["form_key"]]
    schema = current["params"]["requestedSchema"]
    form = current | {
        "params": current["params"]
        | {
            "requestedSchema": schema | {"properties": {"email_column": {"type": "string", "enum": ["contact"]}}},
        }
    }
    calls = []

    class Transport:
        async def request(self, _method, params, *, timeout):
            calls.append(params["arguments"])
            if params["name"] == "pipeline_task_handoff_read":
                return {
                    "isError": False,
                    "structuredContent": {
                        "handoff_id": F["task_id"],
                        "expires_at": "2026-11-19T12:00:00.000Z",
                        "request_key": F["form_key"],
                        "form": form,
                    },
                }
            return {"isError": False, "structuredContent": {"handoff_id": F["task_id"], "submitted": True}}

    reviewer = PipelineTaskHandoffClient(
        F["task_id"], reply_contract=JobContract(F["reply_contract"]), transport=Transport()
    )
    with pytest.raises(PipelineTaskClientError, match="invalid_response"):
        await reviewer.get(timeout=1)
    form = current
    view = await reviewer.get(timeout=1)
    reply = {"email_column": "alternate_contact"}
    with pytest.raises(PipelineTaskClientError, match="invalid_input"):
        await reviewer.answer(view | {"handoff_id": F["request_id"]}, reply, timeout=1)
    assert len(calls) == 2
    await reviewer.answer(view, reply, timeout=1)
    assert calls[-1] == {"handoff_id": F["task_id"], "request_key": F["form_key"], "input": reply}


@pytest.mark.asyncio
async def test_reviewer_inbox_pages_and_refreshes_on_reconnect():
    frames = F["reviewer_inbox"]
    resource_id = "reviewer-test"
    mode, calls = "pending", 0
    sink = None
    closed = asyncio.Event()

    class Transport:
        async def request(self, method, params, *, timeout):
            nonlocal calls
            assert method == "tools/call"
            assert params["name"] == "pipeline_task_handoff_list"
            args = params["arguments"]
            assert args["page_size"] == 1
            calls += 1
            if mode == "unauthorized":
                return {"isError": True, "content": [{"type": "text", "text": '{"code":"not_found"}'}]}
            if "cursor" in args:
                assert args["cursor"] == frames["first"]["next_cursor"]
            page = frames["empty"] if mode == "empty" else frames["next" if "cursor" in args else "first"]
            return {"isError": False, "structuredContent": page}

    async def notifications(observer):
        nonlocal sink
        sink = observer
        sink.ready()
        try:
            await asyncio.Future()
        finally:
            closed.set()

    inbox = PipelineTaskReviewerInbox(Transport())
    assert await inbox.page(page_size=1, timeout=1) == frames["first"]
    async with aclosing(
        inbox.watch(
            timeout=3,
            page_size=1,
            refresh_interval=2,
            notifications=(resource_id, notifications),
        )
    ) as watch:
        expected = {"items": frames["first"]["items"] + frames["next"]["items"], "truncated": False}
        assert await anext(watch) == expected
        mode = "empty"
        event = work_inbox_invalidation(resource_id, "removed")
        sink.changed(event)
        sink.changed(event)
        assert await anext(watch) == {"items": [], "truncated": False}
        assert calls == 4
        sink.changed(event)
        sink.changed(work_inbox_invalidation("another-reviewer", "unrelated"))
        fresh = asyncio.create_task(anext(watch))
        await asyncio.sleep(0.02)
        assert calls == 4
        sink.disconnected()
        mode = "pending"
        sink.ready()
        assert await fresh == expected
        mode = "unauthorized"
        sink.changed(work_inbox_invalidation(resource_id, "revoked"))
        with pytest.raises(LiveWorkInboxError, match="unauthorized"):
            await anext(watch)
    await asyncio.wait_for(closed.wait(), timeout=1)


@pytest.mark.asyncio
async def test_reviewer_inbox_watch_is_bounded_and_refuses_malformed_pages():
    frame = F["reviewer_inbox"]["first"]
    body = frame

    class Transport:
        async def request(self, _method, _params, *, timeout):
            return {"isError": False, "structuredContent": body}

    inbox = PipelineTaskReviewerInbox(Transport())
    async with aclosing(inbox.watch(timeout=1, page_size=1, max_pages=1)) as watch:
        assert await anext(watch) == {"items": frame["items"], "truncated": True}
    with pytest.raises(PipelineTaskClientError, match="invalid_response"):
        await inbox.page(cursor=frame["next_cursor"], timeout=1)
    body = {"items": [frame["items"][0] | {"task_id": F["task_id"]}], "next_cursor": None}
    with pytest.raises(PipelineTaskClientError, match="invalid_response"):
        await inbox.page(timeout=1)


@pytest.mark.asyncio
async def test_reviewer_inbox_polling_recovers_lost_mcp_read():
    calls = 0

    class Transport:
        async def request(self, _method, _params, *, timeout):
            nonlocal calls
            calls += 1
            if calls == 1:
                raise OSError("disconnected")
            return {"isError": False, "structuredContent": F["reviewer_inbox"]["empty"]}

    inbox = PipelineTaskReviewerInbox(Transport())
    async with aclosing(inbox.watch(timeout=1, refresh_interval=0.005)) as watch:
        assert await anext(watch) == {"items": [], "truncated": False}
        assert calls == 2


@pytest.mark.asyncio
async def test_approval_client_binds_review_and_returns_durable_decision():
    review = CONTRACT.input(F["input"])
    view = {
        "handoff_id": F["task_id"],
        "expires_at": "2026-11-19T12:00:00.000Z",
        "title": "Import validated rows",
        "review": review,
        "review_sha256": job_value_hash(review),
        "contract_sha256": CONTRACT.sha256,
        "decision_key": "a" * 64,
    }
    receipt = {"handoff_id": F["task_id"], "state": "approved", "decided_at": "2026-11-19T11:50:00.000Z"}
    calls = []

    class Transport:
        async def request(self, _method, params, *, timeout):
            calls.append(params["arguments"])
            result = view if params["name"] == "pipeline_task_handoff_approval" else receipt
            return {"isError": False, "structuredContent": result}

    client = PipelineTaskApprovalClient(F["task_id"], contract=CONTRACT, transport=Transport())
    preview = await client.get(timeout=1)
    assert preview == view
    with pytest.raises(PipelineTaskClientError, match="invalid_input"):
        await client.decide(preview | {"review": {"id": "changed"}}, "approved", {"rows": 2}, timeout=1)
    assert len(calls) == 1
    assert await client.decide(preview, "approved", {"rows": 2}, timeout=1) == receipt
    assert calls[-1] == {
        "handoff_id": F["task_id"],
        "decision_key": view["decision_key"],
        "state": "approved",
        "input": {"rows": 2},
    }
