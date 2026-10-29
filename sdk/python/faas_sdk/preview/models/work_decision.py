from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.work_decision_action import WorkDecisionAction, check_work_decision_action

T = TypeVar("T", bound="WorkDecision")


@_attrs_define
class WorkDecision:
    """Persisted classifier decision for one execution result."""

    classification: str
    action: WorkDecisionAction
    reason: str
    policy_version: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        classification = self.classification

        action: str = self.action

        reason = self.reason

        policy_version = self.policy_version

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "classification": classification,
                "action": action,
                "reason": reason,
                "policy_version": policy_version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        classification = d.pop("classification")

        action = check_work_decision_action(d.pop("action"))

        reason = d.pop("reason")

        policy_version = d.pop("policy_version")

        work_decision = cls(
            classification=classification,
            action=action,
            reason=reason,
            policy_version=policy_version,
        )

        work_decision.additional_properties = d
        return work_decision

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
