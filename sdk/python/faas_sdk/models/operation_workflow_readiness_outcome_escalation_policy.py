from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReadinessOutcomeEscalationPolicy")


@_attrs_define
class OperationWorkflowReadinessOutcomeEscalationPolicy:
    """Opt-in deadline escalation policy frozen at completion. Requires an outcome watch and future outcome deadline.
    Schedules one assigned follow-up on the deadline notification; never executes business actions.

    """

    owner: str
    """OperationWorkflowReadinessOutcomeEscalationPolicy.owner. Retained coordination escalation policy or
    evidence; business interpretation and execution remain application-owned."""
    follow_up_after_seconds: int
    """OperationWorkflowReadinessOutcomeEscalationPolicy.follow_up_after_seconds. Retained coordination escalation
    policy or evidence; business interpretation and execution remain application-owned."""
    note: str | Unset = UNSET
    """OperationWorkflowReadinessOutcomeEscalationPolicy.note. Retained coordination escalation policy or evidence;
    business interpretation and execution remain application-owned."""

    def to_dict(self) -> dict[str, Any]:
        owner = self.owner

        follow_up_after_seconds = self.follow_up_after_seconds

        note = self.note

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "owner": owner,
                "follow_up_after_seconds": follow_up_after_seconds,
            }
        )
        if note is not UNSET:
            field_dict["note"] = note

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        owner = d.pop("owner")

        follow_up_after_seconds = d.pop("follow_up_after_seconds")

        note = d.pop("note", UNSET)

        operation_workflow_readiness_outcome_escalation_policy = cls(
            owner=owner,
            follow_up_after_seconds=follow_up_after_seconds,
            note=note,
        )

        return operation_workflow_readiness_outcome_escalation_policy
