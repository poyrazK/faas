from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_readiness_policy import MCPReadinessPolicy


T = TypeVar("T", bound="MCPReadinessPolicyHistory")


@_attrs_define
class MCPReadinessPolicyHistory:
    """Bounded page of saved environment readiness policy revisions."""

    policies: list[MCPReadinessPolicy]
    next_before_revision: str | Unset = UNSET
    """Use as before_revision for the next page; absent on the last page"""

    def to_dict(self) -> dict[str, Any]:
        policies = []
        for policies_item_data in self.policies:
            policies_item = policies_item_data.to_dict()
            policies.append(policies_item)

        next_before_revision = self.next_before_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "policies": policies,
            }
        )
        if next_before_revision is not UNSET:
            field_dict["next_before_revision"] = next_before_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_policy import MCPReadinessPolicy

        d = dict(src_dict)
        policies = []
        _policies = d.pop("policies")
        for policies_item_data in _policies:
            policies_item = MCPReadinessPolicy.from_dict(policies_item_data)

            policies.append(policies_item)

        next_before_revision = d.pop("next_before_revision", UNSET)

        mcp_readiness_policy_history = cls(
            policies=policies,
            next_before_revision=next_before_revision,
        )

        return mcp_readiness_policy_history
