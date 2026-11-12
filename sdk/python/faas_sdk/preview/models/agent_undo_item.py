from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.agent_undo_item_kind import AgentUndoItemKind, check_agent_undo_item_kind
from ..models.agent_undo_item_result import AgentUndoItemResult, check_agent_undo_item_result
from ..models.agent_undo_item_status import AgentUndoItemStatus, check_agent_undo_item_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="AgentUndoItem")


@_attrs_define
class AgentUndoItem:
    """One agent change in an undo plan or result."""

    activity_id: str
    """The activity entry of the change."""
    method: str
    """The HTTP method of the agent's request."""
    path: str
    """The path of the agent's request."""
    kind: AgentUndoItemKind
    """What kind of change it was."""
    summary: str
    """What the agent changed."""
    created_at: datetime.datetime
    """When the agent made the change."""
    status: AgentUndoItemStatus
    """What undoing it will do, or did."""
    reason: str | Unset = UNSET
    """Why it cannot be undone, or why it was left."""
    result: AgentUndoItemResult | Unset = UNSET
    """On apply, what happened to it."""
    error_code: str | Unset = UNSET
    """On a failed apply, the code of the refusal."""
    error: str | Unset = UNSET
    """On a failed apply, why it was refused."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        activity_id = self.activity_id

        method = self.method

        path = self.path

        kind: str = self.kind

        summary = self.summary

        created_at = self.created_at.isoformat()

        status: str = self.status

        reason = self.reason

        result: str | Unset = UNSET
        if not isinstance(self.result, Unset):
            result = self.result

        error_code = self.error_code

        error = self.error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "activity_id": activity_id,
                "method": method,
                "path": path,
                "kind": kind,
                "summary": summary,
                "created_at": created_at,
                "status": status,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if result is not UNSET:
            field_dict["result"] = result
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        activity_id = d.pop("activity_id")

        method = d.pop("method")

        path = d.pop("path")

        kind = check_agent_undo_item_kind(d.pop("kind"))

        summary = d.pop("summary")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        status = check_agent_undo_item_status(d.pop("status"))

        reason = d.pop("reason", UNSET)

        _result = d.pop("result", UNSET)
        result: AgentUndoItemResult | Unset
        if isinstance(_result, Unset):
            result = UNSET
        else:
            result = check_agent_undo_item_result(_result)

        error_code = d.pop("error_code", UNSET)

        error = d.pop("error", UNSET)

        agent_undo_item = cls(
            activity_id=activity_id,
            method=method,
            path=path,
            kind=kind,
            summary=summary,
            created_at=created_at,
            status=status,
            reason=reason,
            result=result,
            error_code=error_code,
            error=error,
        )

        agent_undo_item.additional_properties = d
        return agent_undo_item

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
