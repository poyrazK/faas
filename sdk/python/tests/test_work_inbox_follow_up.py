import json
from copy import deepcopy
from pathlib import Path

import pytest
from faas_sdk.preview import (
    ApplicationToolkit,
    ApplicationToolkitError,
    PipelineScope,
    QueryActor,
    QueryMCPError,
    QueryToolError,
    ToolkitAccess,
    ToolkitMount,
    WorkInboxFollowUpOptions,
    WorkInboxSelector,
    WorkInboxSource,
    create_work_inbox,
)
from faas_sdk.preview._pipeline_tool_protocol import WIRE

CASES = json.loads(
    (Path(__file__).resolve().parents[2] / "request-contracts/work-inbox-follow-up-cases.json").read_text()
)
ACCESS = ToolkitAccess("alice", ["inbox:read", "status:read", "refund:read", "refund:reconcile"])
READ = ToolkitAccess("alice", ["inbox:read", "status:read", "refund:read"])
FOLLOW_UP = "work_inbox_follow_up"


class Fixture:
    def __init__(self):
        self.review = deepcopy(CASES["review_entry"])
        self.refund = deepcopy(CASES["refund_entry"])
        self.revoked = False
        self.reads = self.lookups = 0
        self.start_match = {"state": "start_unknown", "request_id": self.review["request_id"]}

        async def authorize(_filters, principal, _check):
            return None if self.revoked else QueryActor(principal, principal)

        async def reference(_candidate, _actor, _check):
            return "import-1"

        async def review(*_):
            return self.review

        async def refund(*_):
            return self.refund

        async def lookup(request_id, access, _timeout):
            assert request_id == self.review["request_id"] and access == "alice"
            self.lookups += 1
            return self.start_match

        inbox = create_work_inbox(
            scope=PipelineScope.from_dict(CASES["scope"]),
            cursor_key="fixture-key-32-bytes-long-fixture-key",
            store=self,
            authorize=authorize,
            business_reference=reference,
            sources=[
                WorkInboxSource(
                    WorkInboxSelector("pipeline", "recover_csv_cleanup", "a" * 64, "csv_cleanup"),
                    review,
                    find_accepted_run=lookup,
                ),
                WorkInboxSource(
                    WorkInboxSelector("action", "csv_refund"),
                    refund,
                    "action_csv_refund",
                ),
            ],
        )
        bindings = [
            ("recover_csv_cleanup_status", "request_id", "status:read"),
            ("action_csv_refund_observe", "action_id", "refund:read"),
            ("action_csv_refund_refresh", "action_id", "refund:reconcile"),
        ]
        self.tools = [
            {
                "name": name,
                "inputSchema": {
                    "type": "object",
                    "additionalProperties": False,
                    "required": [key],
                    "properties": {
                        key: WIRE["request_id_schema"]
                        if key == "request_id"
                        else {"type": "string", "minLength": 64, "maxLength": 64}
                    },
                },
            }
            for name, key, _ in bindings
        ]
        reconcile = "recover_csv_cleanup_reconcile"
        self.tools.append(
            {
                "name": reconcile,
                "inputSchema": {
                    "type": "object",
                    "additionalProperties": False,
                    "required": ["request_id", "run_id"],
                    "properties": {key: WIRE["request_id_schema"] for key in ("request_id", "run_id")},
                },
            }
        )
        rules = {name: [scope] for name, _, scope in bindings} | {reconcile: ["cleanup:recover"]}
        self.toolkit = ApplicationToolkit(
            [ToolkitMount(self, tool_scopes=rules)],
            work_inbox_follow_up=WorkInboxFollowUpOptions(inbox, ["inbox:read"]),
        )

    async def scan(self, *_):
        pytest.fail("follow-up must not page through receipts")

    async def has_receipt(self, ref, check):
        check()
        self.reads += 1
        if ref.actor.owner_id != "alice":
            return False
        if ref.selector.kind == "pipeline":
            return ref.selector.receipt_subject_id == "csv_cleanup" and ref.request_id == self.review["request_id"]
        return ref.request_id == self.refund["request_id"] and ref.identity == CASES["refund_entry"]["action_id"]

    async def call(self, _name, _args, _access, *, timeout):
        pytest.fail("planning invoked a tool")


async def test_resolves_shared_review_and_refund_plans_from_current_state_without_calling_tools():
    f = Fixture()
    assert await f.toolkit.resolve_work_inbox_follow_up(f.review, ACCESS, timeout=1) == CASES["review"]
    plan = await f.toolkit.call(FOLLOW_UP, CASES["action_ref"], ACCESS, timeout=1)
    assert plan["isError"] is False and plan["structuredContent"] == CASES["refresh"]
    stale = f.refund
    f.refund = f.refund | {"state": "succeeded", "next_step": "read_result"}
    assert await f.toolkit.resolve_work_inbox_follow_up(stale, READ, timeout=1) == CASES["observe"]
    f.review = f.review | {"state": "start_unknown", "next_step": "reconcile_start"}
    assert await f.toolkit.resolve_work_inbox_follow_up(CASES["review_ref"], ACCESS, timeout=1) == CASES["no_action"]


async def test_withholds_follow_up_for_other_viewers_wrong_business_records_and_missing_scopes():
    f = Fixture()
    assert FOLLOW_UP not in [tool["name"] for tool in f.toolkit.list_tools(ToolkitAccess("alice", []))]
    with pytest.raises(ApplicationToolkitError, match="tool_not_found"):
        await f.toolkit.resolve_work_inbox_follow_up(CASES["action_ref"], ToolkitAccess("alice", []), timeout=1)
    assert f.reads == 0
    for ref, access in (
        (CASES["action_ref"], READ),
        (CASES["action_ref"], ToolkitAccess("bob", ACCESS.scopes)),
        (CASES["action_ref"] | {"business_ref": "another-import"}, ACCESS),
    ):
        result = await f.toolkit.call(FOLLOW_UP, ref, access, timeout=1)
        assert result["isError"] is True and "structuredContent" not in result
        assert json.loads(result["content"][0]["text"]) == {"code": "not_found"}
    f.revoked = True
    with pytest.raises(QueryToolError, match="not_found"):
        await f.toolkit.resolve_work_inbox_follow_up(CASES["review_ref"], ACCESS, timeout=1)


async def test_validates_mcp_references_and_refuses_unsafe_review_links():
    f = Fixture()
    with pytest.raises(QueryMCPError) as info:
        await f.toolkit.call(FOLLOW_UP, CASES["review_ref"] | {"state": "succeeded"}, ACCESS, timeout=1)
    assert info.value.code == -32602
    assert f.reads == 0
    f.review["review"]["review_url"] += "&token=secret"
    with pytest.raises(QueryToolError, match="invalid_output"):
        await f.toolkit.resolve_work_inbox_follow_up(CASES["review_ref"], ACCESS, timeout=1)


async def test_plans_verified_start_reconciliation_only_with_the_destination_scope():
    f = Fixture()
    operator = ToolkitAccess("alice", [*ACCESS.scopes, "cleanup:recover"])
    f.review = f.review | {"state": "start_unknown", "next_step": "reconcile_start"}
    assert await f.toolkit.resolve_work_inbox_follow_up(CASES["review_ref"], operator, timeout=1) == CASES["no_action"]
    f.start_match = {"state": "found", **CASES["start_reconcile"]["arguments"]}
    assert await f.toolkit.resolve_work_inbox_follow_up(CASES["review_ref"], ACCESS, timeout=1) == CASES["no_action"]
    assert f.lookups == 1
    result = await f.toolkit.call(FOLLOW_UP, CASES["review_ref"], operator, timeout=1)
    assert result["structuredContent"] == CASES["start_reconcile"] and f.lookups == 2
