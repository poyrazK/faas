from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_advice_policy_assessment_status import (
    RouteAdvicePolicyAssessmentStatus,
    check_route_advice_policy_assessment_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteAdvicePolicyAssessment")


@_attrs_define
class RouteAdvicePolicyAssessment:
    """Comparison of a diagnostic suggestion with observed selected-app policy. No status establishes mutation safety."""

    status: RouteAdvicePolicyAssessmentStatus
    """Duplicate means the matching configured payload agrees; conflict means different configured settings.
    Conditional, overlapping or ambiguous rules require review. Missing evidence never means policy absence."""
    reason: str
    """Stable explanation code for this suggestion's policy assessment."""
    rule_ids: list[str]
    """Bounded matching identifiers from this report's observed policy snapshot."""
    truncated: bool | Unset = UNSET
    """More matching policy identifiers exist than are returned."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        reason = self.reason

        rule_ids = self.rule_ids

        truncated = self.truncated

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "reason": reason,
                "rule_ids": rule_ids,
            }
        )
        if truncated is not UNSET:
            field_dict["truncated"] = truncated

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_route_advice_policy_assessment_status(d.pop("status"))

        reason = d.pop("reason")

        rule_ids = cast(list[str], d.pop("rule_ids"))

        truncated = d.pop("truncated", UNSET)

        route_advice_policy_assessment = cls(
            status=status,
            reason=reason,
            rule_ids=rule_ids,
            truncated=truncated,
        )

        route_advice_policy_assessment.additional_properties = d
        return route_advice_policy_assessment

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
