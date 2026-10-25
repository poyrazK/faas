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
    acknowledge_after_seconds: int | Unset = UNSET
    """OperationWorkflowReadinessOutcomeEscalationPolicy.acknowledge_after_seconds. Optional positive response
    target from escalation delivery; zero or omission disables it. Bounded to one week."""
    fallback_owner: str | Unset = UNSET
    """OperationWorkflowReadinessOutcomeEscalationPolicy.fallback_owner. Optional different owner label requiring a
    response target. Applied once only to open unacknowledged escalation with unchanged original ownership and no
    assignment history."""
    note: str | Unset = UNSET
    """OperationWorkflowReadinessOutcomeEscalationPolicy.note. Retained coordination escalation policy or evidence;
    business interpretation and execution remain application-owned."""

    def to_dict(self) -> dict[str, Any]:
        owner = self.owner

        follow_up_after_seconds = self.follow_up_after_seconds

        acknowledge_after_seconds = self.acknowledge_after_seconds

        fallback_owner = self.fallback_owner

        note = self.note

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "owner": owner,
                "follow_up_after_seconds": follow_up_after_seconds,
            }
        )
        if acknowledge_after_seconds is not UNSET:
            field_dict["acknowledge_after_seconds"] = acknowledge_after_seconds
        if fallback_owner is not UNSET:
            field_dict["fallback_owner"] = fallback_owner
        if note is not UNSET:
            field_dict["note"] = note

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        owner = d.pop("owner")

        follow_up_after_seconds = d.pop("follow_up_after_seconds")

        acknowledge_after_seconds = d.pop("acknowledge_after_seconds", UNSET)

        fallback_owner = d.pop("fallback_owner", UNSET)

        note = d.pop("note", UNSET)

        operation_workflow_readiness_outcome_escalation_policy = cls(
            owner=owner,
            follow_up_after_seconds=follow_up_after_seconds,
            acknowledge_after_seconds=acknowledge_after_seconds,
            fallback_owner=fallback_owner,
            note=note,
        )

        return operation_workflow_readiness_outcome_escalation_policy
