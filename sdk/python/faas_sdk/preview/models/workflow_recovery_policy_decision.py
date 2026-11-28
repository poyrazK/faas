from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRecoveryPolicyDecision")


@_attrs_define
class WorkflowRecoveryPolicyDecision:
    """Frozen per-target explanation including eligibility refusals and policy suppression. The resume count identifies the
    reviewed admitted generation.

    """

    position: int
    run_id: UUID
    resume_count: int
    trigger: str
    action: str
    outcome: str
    failure_status: str | Unset = UNSET
    """Exact run status observed at candidate evaluation when available."""
    preflight_outcome: str | Unset = UNSET
    """Eligibility result before policy preparation limits."""
    preflight_blockers: list[str] | Unset = UNSET
    """Frozen preflight blocker codes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        position = self.position

        run_id = str(self.run_id)

        resume_count = self.resume_count

        trigger = self.trigger

        action = self.action

        outcome = self.outcome

        failure_status = self.failure_status

        preflight_outcome = self.preflight_outcome

        preflight_blockers: list[str] | Unset = UNSET
        if not isinstance(self.preflight_blockers, Unset):
            preflight_blockers = self.preflight_blockers

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "position": position,
                "run_id": run_id,
                "resume_count": resume_count,
                "trigger": trigger,
                "action": action,
                "outcome": outcome,
            }
        )
        if failure_status is not UNSET:
            field_dict["failure_status"] = failure_status
        if preflight_outcome is not UNSET:
            field_dict["preflight_outcome"] = preflight_outcome
        if preflight_blockers is not UNSET:
            field_dict["preflight_blockers"] = preflight_blockers

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        position = d.pop("position")

        run_id = UUID(d.pop("run_id"))

        resume_count = d.pop("resume_count")

        trigger = d.pop("trigger")

        action = d.pop("action")

        outcome = d.pop("outcome")

        failure_status = d.pop("failure_status", UNSET)

        preflight_outcome = d.pop("preflight_outcome", UNSET)

        preflight_blockers = cast(list[str], d.pop("preflight_blockers", UNSET))

        workflow_recovery_policy_decision = cls(
            position=position,
            run_id=run_id,
            resume_count=resume_count,
            trigger=trigger,
            action=action,
            outcome=outcome,
            failure_status=failure_status,
            preflight_outcome=preflight_outcome,
            preflight_blockers=preflight_blockers,
        )

        workflow_recovery_policy_decision.additional_properties = d
        return workflow_recovery_policy_decision

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
