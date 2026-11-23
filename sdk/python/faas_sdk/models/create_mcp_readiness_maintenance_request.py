from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateMCPReadinessMaintenanceRequest")


@_attrs_define
class CreateMCPReadinessMaintenanceRequest:
    """Exact-environment notification silence with a finite time range and audit reason."""

    ends_at: datetime.datetime
    """Future end within 720 hours of start."""
    reason: str
    """Nonblank UTF-8 reason of at most 500 bytes."""
    scope: str | Unset = "default"
    starts_at: datetime.datetime | Unset = UNSET
    """Omit to start now. Past starts older than one minute are rejected."""

    def to_dict(self) -> dict[str, Any]:
        ends_at = self.ends_at.isoformat()

        reason = self.reason

        scope = self.scope

        starts_at: str | Unset = UNSET
        if not isinstance(self.starts_at, Unset):
            starts_at = self.starts_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "ends_at": ends_at,
                "reason": reason,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope
        if starts_at is not UNSET:
            field_dict["starts_at"] = starts_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ends_at = datetime.datetime.fromisoformat(d.pop("ends_at"))

        reason = d.pop("reason")

        scope = d.pop("scope", UNSET)

        _starts_at = d.pop("starts_at", UNSET)
        starts_at: datetime.datetime | Unset
        if isinstance(_starts_at, Unset):
            starts_at = UNSET
        else:
            starts_at = datetime.datetime.fromisoformat(_starts_at)

        create_mcp_readiness_maintenance_request = cls(
            ends_at=ends_at,
            reason=reason,
            scope=scope,
            starts_at=starts_at,
        )

        return create_mcp_readiness_maintenance_request
