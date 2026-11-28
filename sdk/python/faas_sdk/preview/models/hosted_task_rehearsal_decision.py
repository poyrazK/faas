from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.hosted_task_rehearsal_decision_decision import (
    HostedTaskRehearsalDecisionDecision,
    check_hosted_task_rehearsal_decision_decision,
)

T = TypeVar("T", bound="HostedTaskRehearsalDecision")


@_attrs_define
class HostedTaskRehearsalDecision:
    """A separate owner decision against an immutable rehearsal plan."""

    plan_sha256: str
    decision: HostedTaskRehearsalDecisionDecision

    def to_dict(self) -> dict[str, Any]:
        plan_sha256 = self.plan_sha256

        decision: str = self.decision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "plan_sha256": plan_sha256,
                "decision": decision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        plan_sha256 = d.pop("plan_sha256")

        decision = check_hosted_task_rehearsal_decision_decision(d.pop("decision"))

        hosted_task_rehearsal_decision = cls(
            plan_sha256=plan_sha256,
            decision=decision,
        )

        return hosted_task_rehearsal_decision
