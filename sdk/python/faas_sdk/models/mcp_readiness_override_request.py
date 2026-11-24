from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="MCPReadinessOverrideRequest")


@_attrs_define
class MCPReadinessOverrideRequest:
    """Request to record an audited readiness override for a deployment."""

    expected_revision: UUID
    reason: str

    def to_dict(self) -> dict[str, Any]:
        expected_revision = str(self.expected_revision)

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_revision = UUID(d.pop("expected_revision"))

        reason = d.pop("reason")

        mcp_readiness_override_request = cls(
            expected_revision=expected_revision,
            reason=reason,
        )

        return mcp_readiness_override_request
