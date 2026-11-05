from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPReadinessMaintenanceWindow")


@_attrs_define
class MCPReadinessMaintenanceWindow:
    """Retained maintenance interval with its creator and optional cancellation attribution."""

    id: UUID
    scope: str
    starts_at: datetime.datetime
    ends_at: datetime.datetime
    reason: str
    created_by: UUID
    """Account that scheduled the window."""
    created_at: datetime.datetime
    cancelled_at: datetime.datetime | Unset = UNSET
    cancelled_by: UUID | Unset = UNSET
    cancel_reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        scope = self.scope

        starts_at = self.starts_at.isoformat()

        ends_at = self.ends_at.isoformat()

        reason = self.reason

        created_by = str(self.created_by)

        created_at = self.created_at.isoformat()

        cancelled_at: str | Unset = UNSET
        if not isinstance(self.cancelled_at, Unset):
            cancelled_at = self.cancelled_at.isoformat()

        cancelled_by: str | Unset = UNSET
        if not isinstance(self.cancelled_by, Unset):
            cancelled_by = str(self.cancelled_by)

        cancel_reason = self.cancel_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "scope": scope,
                "starts_at": starts_at,
                "ends_at": ends_at,
                "reason": reason,
                "created_by": created_by,
                "created_at": created_at,
            }
        )
        if cancelled_at is not UNSET:
            field_dict["cancelled_at"] = cancelled_at
        if cancelled_by is not UNSET:
            field_dict["cancelled_by"] = cancelled_by
        if cancel_reason is not UNSET:
            field_dict["cancel_reason"] = cancel_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        scope = d.pop("scope")

        starts_at = datetime.datetime.fromisoformat(d.pop("starts_at"))

        ends_at = datetime.datetime.fromisoformat(d.pop("ends_at"))

        reason = d.pop("reason")

        created_by = UUID(d.pop("created_by"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _cancelled_at = d.pop("cancelled_at", UNSET)
        cancelled_at: datetime.datetime | Unset
        if isinstance(_cancelled_at, Unset):
            cancelled_at = UNSET
        else:
            cancelled_at = datetime.datetime.fromisoformat(_cancelled_at)

        _cancelled_by = d.pop("cancelled_by", UNSET)
        cancelled_by: UUID | Unset
        if isinstance(_cancelled_by, Unset):
            cancelled_by = UNSET
        else:
            cancelled_by = UUID(_cancelled_by)

        cancel_reason = d.pop("cancel_reason", UNSET)

        mcp_readiness_maintenance_window = cls(
            id=id,
            scope=scope,
            starts_at=starts_at,
            ends_at=ends_at,
            reason=reason,
            created_by=created_by,
            created_at=created_at,
            cancelled_at=cancelled_at,
            cancelled_by=cancelled_by,
            cancel_reason=cancel_reason,
        )

        mcp_readiness_maintenance_window.additional_properties = d
        return mcp_readiness_maintenance_window

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
