from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="RestoreMCPReadinessPolicyRequest")


@_attrs_define
class RestoreMCPReadinessPolicyRequest:
    """Historical policy restore request guarded by the expected current revision and an audit reason."""

    expected_revision: str
    reason: str
    scope: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        reason = self.reason

        scope = self.scope

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "reason": reason,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        reason = d.pop("reason")

        scope = d.pop("scope", UNSET)

        restore_mcp_readiness_policy_request = cls(
            expected_revision=expected_revision,
            reason=reason,
            scope=scope,
        )

        return restore_mcp_readiness_policy_request
