from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="UpdateIssueImpactAlertPolicyRequest")


@_attrs_define
class UpdateIssueImpactAlertPolicyRequest:
    """Set the minimum verified distinct customer count; zero disables the policy."""

    minimum_customers: int

    def to_dict(self) -> dict[str, Any]:
        minimum_customers = self.minimum_customers

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "minimum_customers": minimum_customers,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        minimum_customers = d.pop("minimum_customers")

        update_issue_impact_alert_policy_request = cls(
            minimum_customers=minimum_customers,
        )

        return update_issue_impact_alert_policy_request
