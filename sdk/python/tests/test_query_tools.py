import asyncio
import json
from pathlib import Path

import pytest
from faas_sdk.job_runtime import JobContract
from faas_sdk.preview.pipeline_state import PipelineScope
from faas_sdk.preview.query_tool_mcp import QueryMCPError, QueryToolMCP
from faas_sdk.preview.query_tools import QueryActor, QueryResource, QueryTool, QueryToolError

F = json.loads((Path(__file__).resolve().parents[2] / "request-contracts/query-tools.json").read_text())


class Fixture:
    def __init__(self, **overrides):
        self.revoked, self.calls, self.auth, self.reads, self.resource_owner = False, 0, 0, 0, "alice"
        self.hook, self.change_owner = None, False

        async def authorize(input, access, check):
            self.auth += 1
            return None if self.revoked else QueryActor(access, access)

        async def load(input, context):
            self.calls += 1
            if self.hook:
                self.hook()
            rows = F["rows"] if context.actor.owner_id == "alice" else [dict(id="bob", value="café bob")]
            rows = [r for r in rows if input["search"] in r["value"]]
            start = 0 if context.after is None else next(i for i, r in enumerate(rows) if r["id"] == context.after) + 1
            items = rows[start : start + context.limit]
            return dict(items=items, after=items[-1]["id"] if start + len(items) < len(rows) else None)

        self.definition = (
            dict(
                name="csv_rows",
                description="Search imported rows.",
                revision="v1",
                scope=PipelineScope.from_dict(F["scope"]),
                cursor_key=F["key"],
                authorize=authorize,
                load=load,
            )
            | overrides
        )
        self.tool = QueryTool(JobContract(F["contract"]), **self.definition)

        async def resource_auth(id, access, check):
            return (
                QueryActor(access, access)
                if not self.revoked and id == "report_1" and access == self.resource_owner
                else None
            )

        async def read(id, actor, check):
            self.reads += 1
            if self.change_owner:
                self.resource_owner = "bob"
            return "report body"

        self.resource = QueryResource(
            name="csv_report",
            description="Read an authorized CSV report.",
            revision="v1",
            scope=PipelineScope.from_dict(F["scope"]),
            mime_type="text/csv",
            authorize=resource_auth,
            read=read,
        )


async def test_shared_contract_cursor_descriptors_and_resources():
    f = Fixture()
    mcp = QueryToolMCP(f.tool, resources=[f.resource])
    assert await f.tool.page(F["input"], "alice", timeout=1, page_size=1) == F["first_page"]
    assert mcp.tools == F["tools"] and mcp.resource_templates == F["resource_templates"]
    assert await f.resource.link("report_1", "alice", timeout=1) == F["resource_link"]
    next_page = await f.tool.page(F["input"], "alice", timeout=1, page_size=1, after=F["first_page"]["next_cursor"])
    assert next_page["items"][0]["id"] == "9007199254740995"


async def test_concurrent_principals_and_query_inputs_are_isolated():
    f = Fixture()
    pages = await asyncio.gather(
        *(f.tool.page(F["input"], "bob" if i % 2 else "alice", timeout=1, page_size=1) for i in range(12))
    )
    assert all(p["items"][0]["id"] == ("bob" if i % 2 else F["rows"][0]["id"]) for i, p in enumerate(pages))
    assert f.calls == 12
    with pytest.raises(QueryToolError, match="invalid_cursor"):
        await f.tool.page(F["input"], "bob", timeout=1, after=F["first_page"]["next_cursor"])


async def test_cursor_tampering_and_changed_bindings():
    f = Fixture()
    cursor = F["first_page"]["next_cursor"]
    for after in ("", cursor + "x", "A" + cursor[1:]):
        with pytest.raises(QueryToolError, match="invalid_cursor"):
            await f.tool.page(F["input"], "alice", timeout=1, after=after)
    with pytest.raises(QueryToolError, match="invalid_cursor"):
        await f.tool.page(dict(search="changed"), "alice", timeout=1, after=cursor)
    for change in [
        dict(revision="v2"),
        dict(cursor_key="x" * 32),
        dict(scope=PipelineScope.from_dict(F["scope"] | dict(app_slug="other"))),
    ]:
        other = QueryTool(JobContract(F["contract"]), **(f.definition | change))
        with pytest.raises(QueryToolError, match="invalid_cursor"):
            await other.page(F["input"], "alice", timeout=1, after=cursor)


async def test_revoked_during_load_and_between_pages():
    f = Fixture()
    f.hook = lambda: setattr(f, "revoked", True)
    with pytest.raises(QueryToolError, match="unauthorized"):
        await f.tool.page(F["input"], "alice", timeout=1)
    f.hook, f.revoked = None, False
    stream = f.tool.pages(F["input"], "alice", timeout=1, page_size=1)
    await anext(stream)
    f.revoked = True
    with pytest.raises(QueryToolError, match="unauthorized"):
        await anext(stream)
    assert f.calls == 2


@pytest.mark.parametrize("page", F["invalid_pages"])
async def test_shared_invalid_provider_pages(page):
    async def load(input, context):
        return page

    with pytest.raises(QueryToolError, match="invalid_output"):
        await Fixture(load=load).tool.page(F["input"], "alice", timeout=1, page_size=1)


async def test_lazy_iteration_budgets_and_continuation():
    f = Fixture()
    stream = f.tool.pages(F["input"], "alice", timeout=1, page_size=1, max_pages=1)
    assert f.calls == 0
    pages = [p async for p in stream]
    assert f.calls == 1 and len(pages) == 1 and pages[0]["next_cursor"]
    items = [item async for item in f.tool.items(F["input"], "alice", timeout=1, page_size=2, max_items=2)]
    assert len(items) == 2 and f.calls == 2


async def test_total_deadline_includes_paused_iteration():
    f = Fixture()
    stream = f.tool.pages(F["input"], "alice", timeout=0.02, page_size=1)
    await anext(stream)
    await asyncio.sleep(0.03)
    with pytest.raises(QueryToolError, match="deadline"):
        await anext(stream)
    assert f.calls == 1


async def test_deadline_cancellation_and_private_errors():
    started = asyncio.Event()

    async def load(input, context):
        started.set()
        await asyncio.Event().wait()

    f = Fixture(load=load)
    with pytest.raises(QueryToolError, match="deadline"):
        await f.tool.page(F["input"], "alice", timeout=0.02)
    assert f.auth == 1
    task = asyncio.create_task(f.tool.page(F["input"], "alice", timeout=1))
    started.clear()
    await started.wait()
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task

    async def fail(input, context):
        raise RuntimeError("private SQL and credentials")

    with pytest.raises(QueryToolError, match="query_failed") as info:
        await Fixture(load=fail).tool.page(F["input"], "alice", timeout=1)
    assert "private" not in str(info.value)


async def test_bounds_and_repeated_positions():
    with pytest.raises(QueryToolError, match="limit"):
        await Fixture(max_bytes=1).tool.page(F["input"], "alice", timeout=1)
    f = Fixture()
    for page_size in (0, 101, 1.5, True):
        with pytest.raises(QueryToolError, match="invalid_input"):
            await f.tool.page(F["input"], "alice", timeout=1, page_size=page_size)

    async def repeated(input, context):
        return dict(items=F["rows"][:1], after=F["rows"][0]["id"])

    with pytest.raises(QueryToolError, match="invalid_output"):
        await Fixture(load=repeated).tool.page(F["input"], "alice", timeout=1, after=F["first_page"]["next_cursor"])


async def test_resource_access_and_non_fetchable_uris():
    f = Fixture()
    with pytest.raises(QueryToolError, match="unauthorized"):
        await f.resource.link("report_1", "bob", timeout=1)
    link = await f.resource.link("report_1", "alice", timeout=1)
    assert (await f.resource.read(link["uri"], "alice", timeout=1))["text"] == "report body"
    for uri in (
        "https://evil.test/",
        link["uri"] + "?token=1",
        link["uri"] + "/../x",
        link["uri"].replace("csv_report", "other"),
    ):
        with pytest.raises(QueryToolError, match="not_found"):
            await f.resource.read(uri, "alice", timeout=1)
    assert f.reads == 1
    f.change_owner = True
    for _ in range(2):
        with pytest.raises(QueryToolError, match="unauthorized"):
            await f.resource.read(link["uri"], "alice", timeout=1)


async def test_mcp_typed_pages_links_protocol_and_strict_arguments():
    f = Fixture()
    mcp = QueryToolMCP(
        f.tool, resources=[f.resource], links=lambda item: [dict(resource=f.resource.name, id="report_1")]
    )
    result = await mcp.call(f.tool.name, dict(input=F["input"], page_size=1), "alice", timeout=1)
    assert result["structuredContent"] == F["first_page"] and result["content"][1] == F["resource_link"]
    assert (await mcp.read_resource(F["resource_link"]["uri"], "alice", timeout=1))["contents"][0][
        "text"
    ] == "report body"
    with pytest.raises(QueryMCPError) as info:
        await mcp.read_resource(F["resource_link"]["uri"], "bob", timeout=1)
    assert info.value.code == -32002
    with pytest.raises(QueryMCPError) as info:
        await mcp.call("unknown", {}, "alice", timeout=1)
    assert info.value.code == -32601
    for args in [
        dict(input=F["input"], owner_id="alice"),
        dict(input=dict(search=1)),
        dict(input=F["input"], page_size=True),
        dict(input=F["input"], after=None),
    ]:
        with pytest.raises(QueryMCPError) as info:
            await mcp.call(f.tool.name, args, "alice", timeout=1)
        assert info.value.code == -32602
    f.revoked = True
    assert (await mcp.call(f.tool.name, dict(input=F["input"]), "alice", timeout=1))["content"] == [
        dict(type="text", text='{"code":"unauthorized"}')
    ]
    f.revoked = False
    assert (
        await QueryToolMCP(f.tool, protocol_version="2026-07-28").call(
            f.tool.name, dict(input=F["input"]), "alice", timeout=1
        )
    )["resultType"] == "complete"


async def test_complete_mcp_frames_and_resource_bytes_are_bounded():
    f = Fixture()

    async def authorize(input, access, check):
        return QueryActor("alice", "alice")

    async def load(input, context):
        return dict(items=["x" * 600000], after=None)

    huge = QueryTool(
        JobContract(
            dict(
                version=1, name="query-large", input=dict(type="string"), output=dict(type="string", maxLength=1000000)
            )
        ),
        **(f.definition | dict(authorize=authorize, load=load, max_bytes=1048576)),
    )
    assert len((await huge.page("input", "alice", timeout=1))["items"]) == 1
    result = await QueryToolMCP(huge).call(huge.name, dict(input="input"), "alice", timeout=1)
    assert result["isError"] and result["content"] == [dict(type="text", text='{"code":"limit"}')]

    async def read(id, actor, check):
        return "x" * 100

    base = dict(
        name="large_report",
        description="Read a report.",
        revision="v1",
        scope=PipelineScope.from_dict(F["scope"]),
        mime_type="text/plain",
        authorize=authorize,
        read=read,
    )
    small = QueryResource(**base, max_bytes=1)
    link = await small.link("one", "alice", timeout=1)
    with pytest.raises(QueryToolError, match="limit"):
        await small.read(link["uri"], "alice", timeout=1)

    async def escaped_read(id, actor, check):
        return chr(34) * 600000

    escaped = QueryResource(**(base | dict(read=escaped_read)), max_bytes=1048576)
    link = await escaped.link("one", "alice", timeout=1)
    assert len((await escaped.read(link["uri"], "alice", timeout=1))["text"]) == 600000
    with pytest.raises(QueryMCPError) as info:
        await QueryToolMCP(f.tool, resources=[escaped]).read_resource(link["uri"], "alice", timeout=1)
    assert info.value.code == -32603
