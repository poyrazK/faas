import asyncio
import json
from dataclasses import replace
from pathlib import Path

import pytest

from faas_sdk.preview import (
    ApplicationToolkit,
    PipelineTaskCaller,
    PipelineTaskRecoveryPolicy,
    PipelineTasks,
    QueryToolError,
    ToolkitAccess,
    ToolkitMount,
)

C = json.loads((Path(__file__).resolve().parents[2] / "request-contracts/pipeline-task-cases.json").read_text())
ACCESS = ToolkitAccess("alice", ["start", "status", "result"])
ALICE = PipelineTaskCaller("https://app.example.test/mcp", "alice", "agent", ACCESS)
ARGS = {"request_id": C["request_id"], "input": {"rows": 2}}
TOOL = C["tool"]
START = TOOL + "_start"
META = "io.gregale/pipeline"


class Fixture:
    def __init__(self):
        self.bindings = {}
        self.pipeline, self.revoked, self.expired = "start_unknown", False, False
        self.identity = C["receipt_identity"]
        self.calls = self.starts = 0
        self.tools = [{"name": TOOL + "_" + op} for op in ("start", "status", "result")]
        self.toolkit = ApplicationToolkit(
            [ToolkitMount(self, tool_scopes={TOOL + "_" + op: [op] for op in ("start", "status", "result")})]
        )

    def open(self):
        return PipelineTasks(
            self.toolkit,
            store=self,
            namespace="test",
            owner_key="task-key-for-tests-at-least-32-bytes",
            tools=[TOOL],
        )

    async def load(self, namespace, owner, lookup):
        row = self.bindings.get((namespace, owner))
        return row if not self.expired and row and all(row[key] == value for key, value in lookup.items()) else None

    async def bind(self, namespace, owner, row):
        key = (namespace, owner)
        old = self.bindings.get(key)
        if old and old["receipt_identity"] != row["receipt_identity"]:
            return None
        if old and not self.expired:
            return old
        self.expired = False
        self.bindings[key] = row | {"created_at": C["created_at"]}
        return self.bindings[key]

    async def call(self, name, _args, principal, *, timeout):
        self.calls += 1
        if self.revoked or principal != "alice":
            return {"isError": True, "content": [{"type": "text", "text": "denied"}]}
        if name == START:
            self.starts = 1  # The pipeline adapter owns deduplication.
        result = {
            "state": "started" if name == START and self.pipeline != "start_unknown" else self.pipeline,
            "receipt": {
                "tool": TOOL,
                "request_id": C["request_id"],
                "identity": self.identity,
                "run_id": None if self.pipeline == "start_unknown" else C["run_id"],
            },
            "steps": [C["step"]],
            "approval": C["review"] | {"name": "review"} if self.pipeline == "awaiting_approval" else None,
        }
        if name.endswith("_result"):
            result["output"] = C["output"]
        return {
            "isError": False,
            "content": [{"type": "text", "text": json.dumps(result)}],
            "structuredContent": result,
        }


async def test_resumes_one_task_across_clients_and_reports_current_progress_review_and_typed_result():
    f = Fixture()
    first, replay = await asyncio.gather(*(f.open().create(START, ARGS, ALICE, timeout=1) for _ in range(2)))
    assert first["resultType"] == replay["resultType"] == "task"
    task_id = first["taskId"]
    assert replay["taskId"] == task_id and f.starts == 1
    fresh = f.open()
    uncertain = await fresh.get(task_id, ALICE, timeout=1)
    assert uncertain["status"] == "working" and uncertain["_meta"][META]["state"] == "start_unknown"
    f.pipeline = "awaiting_approval"
    pending = await fresh.get(task_id, ALICE, timeout=1)
    assert pending["_meta"][META]["review"] == C["review"]
    assert pending["_meta"][META]["steps"] == [C["step"]]
    f.pipeline = "succeeded"
    completed = await fresh.get(task_id, ALICE, timeout=1)
    assert completed["status"] == "completed" and completed["result"]["structuredContent"]["output"] == C["output"]
    with pytest.raises(QueryToolError, match="not_found"):
        await fresh.get(task_id, replace(ALICE, access=ToolkitAccess("alice", ["start", "status"])), timeout=1)
    f.revoked = True
    with pytest.raises(QueryToolError, match="not_found"):
        await fresh.get(task_id, ALICE, timeout=1)


async def test_rejects_foreign_owners_oauth_clients_changed_receipt_bindings_and_expired_handles():
    f = Fixture()
    runtime = f.open()
    first = await runtime.create(START, ARGS, ALICE, timeout=1)
    task_id, calls = first["taskId"], f.calls
    for caller in (replace(ALICE, subject="bob"), replace(ALICE, client_id="different-agent")):
        with pytest.raises(QueryToolError, match="not_found"):
            await runtime.get(task_id, caller, timeout=1)
    assert f.calls == calls
    f.identity = "f" * 64
    with pytest.raises(QueryToolError, match="not_found"):
        await runtime.get(task_id, ALICE, timeout=1)
    with pytest.raises(QueryToolError, match="not_found"):
        await runtime.create(START, ARGS, ALICE, timeout=1)
    f.expired = True
    assert not runtime.can_create(TOOL + "_result")
    with pytest.raises(QueryToolError, match="not_found"):
        await runtime.get(task_id, ALICE, timeout=1)


async def test_repairs_a_replacement_link_after_failed_bind_and_isolates_oauth_clients():
    token, contract_hash = "2000000000." + "a" * 64, "c" * 64
    bindings, receipts = {}, {C["request_id"]}
    starts, preparations, lose_link, eligible = 0, 0, True, True

    class Store:
        async def load(self, namespace, owner, lookup):
            return next((
                row for (ns, principal, _), row in bindings.items()
                if (ns, principal) == (namespace, owner) and all(row[k] == v for k, v in lookup.items())
            ), None)

        async def bind(self, namespace, owner, row):
            return bindings.setdefault(
                (namespace, owner, row["request_id"]), row | {"created_at": C["created_at"]},
            )

        load_linked = load

        async def bind_linked(self, namespace, owner, row):
            nonlocal lose_link
            if lose_link:
                lose_link = False
                raise RuntimeError("link write interrupted")
            return await self.bind(namespace, owner, row)

    class Toolkit:
        tool_scopes = {TOOL + "_" + op: [op] for op in ("start", "status", "result", "preview")}

        def list_tools(self, _access):
            return [{"name": name} for name in self.tool_scopes]

        def list_pipeline_contracts(self, _access):
            return [{"tool": TOOL, "contract_sha256": contract_hash, "preview": {"type": "integer"}}]

        async def call(self, name, args, _access, *, timeout):
            nonlocal starts
            id = args["request_id"]
            if name.endswith("_preview"):
                content = dict(summary=2, preview_token=token, expires_at=2000000000)
            else:
                if name.endswith("_start") and id not in receipts:
                    starts += 1
                    receipts.add(id)
                state = "started" if name.endswith("_start") else (
                    "not_found" if id not in receipts else "failed" if id == C["request_id"] else "running"
                )
                content = dict(
                    state=state, receipt=dict(tool=TOOL, request_id=id, identity=C["receipt_identity"], run_id=C["run_id"]),
                    steps=[], approval=None,
                )
                if id == C["request_id"]:
                    content["source_failure"] = "source_unavailable"
            return {"isError": False, "structuredContent": content, "content": [{"type": "text", "text": json.dumps(content)}]}

    async def prepare(source, _principal, _timeout):
        nonlocal preparations
        preparations += 1
        assert source["receipt_identity"] == C["receipt_identity"]
        return {"input": {"rows": 2}, "previous_source_sha256": "d" * 64} if eligible else None

    def open_runtime():
        return PipelineTasks(
            Toolkit(), store=Store(), namespace="linked", owner_key="task-key-for-tests-at-least-32-bytes",
            tools=[TOOL], recovery=PipelineTaskRecoveryPolicy(scopes=["replace"], prepare=prepare),
        )

    caller = replace(ALICE, access=ToolkitAccess("alice", [*ACCESS.scopes, "preview", "replace"]))
    runtime = open_runtime()
    original = await runtime.create(START, ARGS, caller, timeout=1)
    preview_args = dict(
        task_id=original["taskId"], recovery_request_id="aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa",
        expected_contract_sha256=contract_hash,
    )
    confirmed = preview_args | {"preview_token": token}
    name = runtime.recovery_tools["confirm"]["name"]
    foreign = await runtime.preview_recovery(preview_args, replace(caller, client_id="other"), timeout=1)
    assert json.loads(foreign["content"][0]["text"]) == {"code": "not_found"}
    assert (await runtime.preview_recovery(preview_args, caller, timeout=1))["structuredContent"]["preview_token"] == token
    assert starts == 0
    with pytest.raises(QueryToolError, match="query_failed"):
        await runtime.create(name, confirmed, caller, timeout=1)
    eligible = False
    recovered = await open_runtime().create(name, confirmed, caller, timeout=1)
    replayed = await runtime.create(name, confirmed, caller, timeout=1)
    assert recovered["taskId"] == replayed["taskId"]
    replacement = await runtime.get(recovered["taskId"], caller, timeout=1)
    assert replacement["_meta"][META]["replaces_task_id"] == original["taskId"]
    assert starts == 1 and preparations == 2
