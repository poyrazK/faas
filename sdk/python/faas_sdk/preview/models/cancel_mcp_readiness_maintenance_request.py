from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="CancelMCPReadinessMaintenanceRequest")


@_attrs_define
class CancelMCPReadinessMaintenanceRequest:
    """Reason for ending a maintenance window before its scheduled expiry."""

    reason: str

    def to_dict(self) -> dict[str, Any]:
        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reason = d.pop("reason")

        cancel_mcp_readiness_maintenance_request = cls(
            reason=reason,
        )

        return cancel_mcp_readiness_maintenance_request
