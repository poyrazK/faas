import asyncio
import base64
import json
from dataclasses import replace
from pathlib import Path

import pytest
from faas_sdk.preview import (
    PipelineScope,
    PipelineToolError,
    QueryActor,
    QueryToolError,
    QueryToolMCP,
    WorkInboxCandidate,
    WorkInboxSelector,
    WorkInboxSource,
    create_work_inbox,
    work_inbox_position,
)
from faas_sdk.preview.pipeline_tool_state import identity as pipeline_tool_identity

FIXTURE = json.loads((Path(__file__).resolve().parents[2] / "request-contracts/work-inbox-cases.json").read_text())
ROWS = [WorkInboxCandidate(**x) for x in FIXTURE["candidates"]]
SELECTORS = [WorkInboxSelector(**x) for x in FIXTURE["selectors"]]


class Store:
    async def scan(self, request, check):
        if request.actor.owner_id != "alice":
            return []
        selected = {(s.kind, s.name) for s in request.selectors}
        return [r for r in ROWS if r.position > (request.after or "") and (r.kind, r.name) in selected][: request.limit]


class Fixture:
    def __init__(self, **overrides):
        self.visible, self.visited = True, []

        async def authorize(input, access, check):
            return QueryActor(access, access) if self.visible else None

        async def inspect(row, actor, scope, access, timeout):
            assert access == actor.subject_id
            self.visited.append(row.identity)
            if row == ROWS[0]:
                raise PipelineToolError("unauthorized")
            return {
                "kind": row.kind,
                "name": row.name,
                "state": "unknown" if row.kind == "action" else "running",
                "next_step": "reconcile_action" if row.kind == "action" else "inspect",
                "request_id": row.request_id,
            }

        async def reference(row, actor, check):
            return "import-1"

        opts = {
            "scope": PipelineScope.from_dict(FIXTURE["scope"]),
            "cursor_key": FIXTURE["key"],
            "scan_limit": 1,
            "sources": [WorkInboxSource(s, inspect) for s in SELECTORS],
            "store": Store(),
            "authorize": authorize,
            "business_reference": reference,
        } | overrides
        self.inbox = create_work_inbox(**opts)


async def test_shared_sparse_page_cursor_and_no_denied_receipt():
    f = Fixture()
    first = await f.inbox.page({}, "alice", timeout=1)
    assert first == FIXTURE["first_page"] and len(f.visited) == 1
    payload = first["next_cursor"].split(".")[0]
    decoded = json.loads(base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4)))
    assert decoded["a"] == ROWS[0].position and ROWS[0].identity not in json.dumps(decoded)
    second = await f.inbox.page({}, "alice", timeout=1, after=first["next_cursor"])
    assert second["items"][0]["request_id"] == ROWS[1].request_id and second["next_cursor"]
    third = await f.inbox.page({}, "alice", timeout=1, after=second["next_cursor"])
    assert third["items"][0]["request_id"] == ROWS[2].request_id and third["next_cursor"] is None


async def test_current_authority_per_principal_and_filter():
    f = Fixture()
    alice, bob = await asyncio.gather(f.inbox.page({}, "alice", timeout=1), f.inbox.page({}, "bob", timeout=1))
    assert bob == {"items": [], "next_cursor": None} and len(f.visited) == 1
    with pytest.raises(QueryToolError, match="invalid_cursor"):
        await f.inbox.page({}, "bob", timeout=1, after=alice["next_cursor"])
    with pytest.raises(QueryToolError, match="invalid_cursor"):
        await f.inbox.page({"kind": "action"}, "alice", timeout=1, after=alice["next_cursor"])
    f.visible = False
    with pytest.raises(QueryToolError, match="unauthorized"):
        await f.inbox.page({}, "alice", timeout=1, after=alice["next_cursor"])


async def test_filters_apply_to_current_state_and_mcp_remains_read_only():
    f = Fixture(scan_limit=10)
    mcp = QueryToolMCP(f.inbox)
    assert mcp.tools[0]["annotations"]["readOnlyHint"]
    page = await mcp.call(
        "work_inbox",
        {"input": {"business_ref": "import-1", "state": "running", "kind": "pipeline"}},
        "alice",
        timeout=1,
    )
    assert not page["isError"]
    items = page["structuredContent"]["items"]
    assert items and all(
        item["business_ref"] == "import-1" and item["request_id"] != ROWS[0].request_id for item in items
    )
    assert (await f.inbox.page({"business_ref": "absent"}, "alice", timeout=1))["items"] == []


async def test_corrupt_candidate_never_reaches_inspector():
    class Corrupt:
        async def scan(self, request, check):
            return [replace(ROWS[0], request_id=ROWS[1].request_id)]

    f = Fixture(store=Corrupt())
    with pytest.raises(QueryToolError, match="invalid_output"):
        await f.inbox.page({}, "alice", timeout=1)
    assert not f.visited


async def test_deadline_and_cancellation_fence_late_scan():
    started, release = asyncio.Event(), asyncio.Event()

    class Blocking:
        async def scan(self, request, check):
            started.set()
            await release.wait()
            return ROWS[:1]

    f = Fixture(store=Blocking())
    with pytest.raises(QueryToolError, match="deadline"):
        await f.inbox.page({}, "alice", timeout=0.02)
    started.clear()
    pending = asyncio.create_task(f.inbox.page({}, "alice", timeout=1))
    await started.wait()
    pending.cancel()
    with pytest.raises(asyncio.CancelledError):
        await pending
    release.set()
    assert not f.visited


async def test_fixed_backend_subject_keeps_the_authenticated_caller_and_owner():
    scope = PipelineScope.from_dict(FIXTURE["scope"])
    selector = replace(next(s for s in SELECTORS if s.kind == "pipeline"), receipt_subject_id="cleanup_namespace")
    original = next(r for r in ROWS if r.kind == "pipeline")
    identity = pipeline_tool_identity(scope, selector.name, "cleanup_namespace", original.request_id)
    row = replace(original, identity=identity, position=work_inbox_position("pipeline", identity))

    async def inspect(candidate, actor, bound_scope, access, timeout):
        assert actor == QueryActor("alice", "alice") and access == "alice" and bound_scope == scope
        return {
            "kind": "pipeline",
            "name": candidate.name,
            "state": "running",
            "next_step": "inspect",
            "request_id": candidate.request_id,
        }

    class NamespacedStore:
        async def scan(self, request, check):
            assert request.selectors == (selector,)
            return [row] if request.actor.owner_id == "alice" else []

    fixture = Fixture(sources=[WorkInboxSource(selector, inspect)], store=NamespacedStore())
    assert (await fixture.inbox.page({}, "alice", timeout=1))["items"][0]["request_id"] == original.request_id
    assert (await fixture.inbox.page({}, "bob", timeout=1))["items"] == []
