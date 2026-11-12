from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.agent_activity_entry_method import AgentActivityEntryMethod, check_agent_activity_entry_method
from ..types import UNSET, Unset

T = TypeVar("T", bound="AgentActivityEntry")


@_attrs_define
class AgentActivityEntry:
    """One write request an agent key made."""

    id: str
    method: AgentActivityEntryMethod
    path: str
    """The request path, without its query string."""
    status: int
    """The HTTP status the request was answered with."""
    created_at: datetime.datetime
    approval_id: str | Unset = UNSET
    """The approval request the write went through on, when it needed one."""
    reason: str | Unset = UNSET
    """What the agent said this write was for (its Gregale-Agent-Reason header), if it said. Untrusted agent text."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        method: str = self.method

        path = self.path

        status = self.status

        created_at = self.created_at.isoformat()

        approval_id = self.approval_id

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "method": method,
                "path": path,
                "status": status,
                "created_at": created_at,
            }
        )
        if approval_id is not UNSET:
            field_dict["approval_id"] = approval_id
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        method = check_agent_activity_entry_method(d.pop("method"))

        path = d.pop("path")

        status = d.pop("status")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        approval_id = d.pop("approval_id", UNSET)

        reason = d.pop("reason", UNSET)

        agent_activity_entry = cls(
            id=id,
            method=method,
            path=path,
            status=status,
            created_at=created_at,
            approval_id=approval_id,
            reason=reason,
        )

        agent_activity_entry.additional_properties = d
        return agent_activity_entry

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
