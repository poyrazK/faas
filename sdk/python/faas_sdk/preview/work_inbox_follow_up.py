"""Plan an explicitly invoked follow-up from a freshly authorized inbox entry."""

from __future__ import annotations

import asyncio
from collections.abc import Callable, Sequence
from dataclasses import dataclass
from typing import Any, Generic, Literal, TypedDict, TypeVar

from ..job_runtime import JobContract, JobError
from ._pipeline_tool_protocol import WIRE
from ._work_inbox_protocol import FOLLOW_UP_SPEC
from .live_work_inbox import _check_review
from .pipeline_state import _hash, _uuid
from .query_tools import QueryToolError
from .work_inbox import WorkInbox, WorkInboxReference, WorkInboxReview, WorkInboxState

A = TypeVar("A")


class WorkInboxHumanReview(TypedDict):
    kind: Literal["human_review"]
    state: Literal["awaiting_approval", "awaiting_input"]
    review: WorkInboxReview


class _RequestArguments(TypedDict):
    request_id: str


class _ReconcileArguments(_RequestArguments):
    run_id: str


class _ActionArguments(TypedDict):
    action_id: str


class WorkInboxToolCall(TypedDict):
    kind: Literal["tool_call"]
    state: WorkInboxState
    name: str
    arguments: _RequestArguments | _ActionArguments | _ReconcileArguments


class WorkInboxNoAction(TypedDict):
    kind: Literal["no_action"]
    state: WorkInboxState


WorkInboxFollowUp = WorkInboxHumanReview | WorkInboxToolCall | WorkInboxNoAction


@dataclass(frozen=True, slots=True)
class WorkInboxFollowUpOptions(Generic[A]):
    inbox: WorkInbox[A]
    scopes: Sequence[str]


_DESCRIPTOR: dict[str, Any] = {
    "name": "work_inbox_follow_up",
    "description": (
        "Resolve a current authorized follow-up from an inbox reference. Returns a human review link, "
        "a tool call to invoke explicitly, or no action."
    ),
    "inputSchema": FOLLOW_UP_SPEC["input"],
    "outputSchema": FOLLOW_UP_SPEC["output"],
    "annotations": {"readOnlyHint": True, "destructiveHint": False, "idempotentHint": True, "openWorldHint": False},
}


_CONTRACT: JobContract[WorkInboxReference, WorkInboxFollowUp] = JobContract(FOLLOW_UP_SPEC)
_ACTION_ID_SCHEMA = {"type": "string", "minLength": 64, "maxLength": 64}


def _arguments(value: Any) -> WorkInboxReference:
    try:
        return _CONTRACT.input(value)
    except JobError:
        raise QueryToolError("invalid_input") from None


async def _resolve(
    inbox: WorkInbox[A],
    reference: WorkInboxReference,
    principal: A,
    timeout: float,
    visible_tool: Callable[[str], dict[str, Any] | None],
) -> WorkInboxFollowUp:
    end = asyncio.get_running_loop().time() + timeout
    try:
        item = await inbox.find_entry(reference, principal, timeout=timeout)
    except QueryToolError as error:
        if error.code == "unauthorized":
            raise QueryToolError("not_found") from None
        raise
    if item is None:
        raise QueryToolError("not_found")
    tool_name = item["name"]
    state = item["state"]

    def published(suffix: str, *keys: str) -> str | None:
        name = tool_name + "_" + suffix
        tool = visible_tool(name)
        schema = tool.get("inputSchema") if tool else None
        properties = schema.get("properties") if isinstance(schema, dict) else None
        required = schema.get("required") if isinstance(schema, dict) else None
        if (
            not isinstance(schema, dict)
            or schema.get("type") != "object"
            or schema.get("additionalProperties") is not False
            or not isinstance(required, list)
            or len(required) != len(keys)
            or any(key not in required for key in keys)
            or not isinstance(properties, dict)
            or set(properties) != set(keys)
            or any(
                properties[key] != (_ACTION_ID_SCHEMA if key == "action_id" else WIRE["request_id_schema"])
                for key in keys
            )
        ):
            return None
        return name

    def destination(suffix: str, key: str) -> str:
        name = published(suffix, key)
        if name is None:
            raise QueryToolError("not_found")
        return name

    if item["kind"] == "pipeline":
        if state == "start_unknown" and (name := published("reconcile", "request_id", "run_id")):
            left = end - asyncio.get_running_loop().time()
            if left <= 0:
                raise QueryToolError("deadline")
            try:
                run_id = await inbox.find_accepted_run(reference, principal, timeout=left)
            except QueryToolError as error:
                if error.code == "unauthorized":
                    raise QueryToolError("not_found") from None
                raise
            if run_id is not None:
                if not _uuid(run_id):
                    raise QueryToolError("invalid_output")
                return WorkInboxToolCall(
                    kind="tool_call",
                    state=state,
                    name=name,
                    arguments=_ReconcileArguments(request_id=item["request_id"], run_id=run_id),
                )
        if state in ("awaiting_approval", "awaiting_input"):
            destination("status", "request_id")
            try:
                if not _uuid(item.get("run_id")):
                    raise ValueError
                _check_review(item)
            except (KeyError, TypeError, ValueError):
                raise QueryToolError("invalid_output") from None
            return WorkInboxHumanReview(kind="human_review", state=state, review=item["review"])
        if state in ("running", "succeeded"):
            return WorkInboxToolCall(
                kind="tool_call",
                state=state,
                name=destination("status" if state == "running" else "result", "request_id"),
                arguments={"request_id": item["request_id"]},
            )
    elif state in ("unknown", "pending", "prepared", "succeeded"):
        action_id = item.get("action_id")
        if not isinstance(action_id, str) or not _hash(action_id):
            raise QueryToolError("invalid_output")
        return WorkInboxToolCall(
            kind="tool_call",
            state=state,
            name=destination("refresh" if state in ("unknown", "pending") else "observe", "action_id"),
            arguments={"action_id": action_id},
        )
    return WorkInboxNoAction(kind="no_action", state=state)
