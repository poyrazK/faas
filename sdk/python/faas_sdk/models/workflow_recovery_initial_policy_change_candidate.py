from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_initial_failure import WorkflowInitialFailure
    from ..models.workflow_run_recovery_target import WorkflowRunRecoveryTarget


T = TypeVar("T", bound="WorkflowRecoveryInitialPolicyChangeCandidate")


@_attrs_define
class WorkflowRecoveryInitialPolicyChangeCandidate:
    """Read-only initial-failure decision for a proposed next policy version. Eligibility reports policy filters and fresh
    safety diagnostics. Preparation reports duplicate, review-retention, cooldown or action-budget pressure. Execution
    reports whether that prepared candidate would be submitted automatically, including durable execution budget,
    cooldown, unresolved app recovery and native account quota. Target is advisory and omitted for excluded or unsafe
    failures; it grants no execution authority.

    """

    failure: WorkflowInitialFailure
    """One captured generation-zero workflow failure."""
    eligibility: str
    preparation: str
    execution: str
    blockers: list[str]
    target: WorkflowRunRecoveryTarget | Unset = UNSET
    """One reviewed workflow run and resume count. Retry children identify the saved parent item. Initial failure
    targets may identify retained generation-zero evidence, which is checked again at creation and admission."""

    def to_dict(self) -> dict[str, Any]:
        failure = self.failure.to_dict()

        eligibility = self.eligibility

        preparation = self.preparation

        execution = self.execution

        blockers = self.blockers

        target: dict[str, Any] | Unset = UNSET
        if not isinstance(self.target, Unset):
            target = self.target.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "failure": failure,
                "eligibility": eligibility,
                "preparation": preparation,
                "execution": execution,
                "blockers": blockers,
            }
        )
        if target is not UNSET:
            field_dict["target"] = target

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_initial_failure import WorkflowInitialFailure
        from ..models.workflow_run_recovery_target import WorkflowRunRecoveryTarget

        d = dict(src_dict)
        failure = WorkflowInitialFailure.from_dict(d.pop("failure"))

        eligibility = d.pop("eligibility")

        preparation = d.pop("preparation")

        execution = d.pop("execution")

        blockers = cast(list[str], d.pop("blockers"))

        _target = d.pop("target", UNSET)
        target: WorkflowRunRecoveryTarget | Unset
        if isinstance(_target, Unset):
            target = UNSET
        else:
            target = WorkflowRunRecoveryTarget.from_dict(_target)

        workflow_recovery_initial_policy_change_candidate = cls(
            failure=failure,
            eligibility=eligibility,
            preparation=preparation,
            execution=execution,
            blockers=blockers,
            target=target,
        )

        return workflow_recovery_initial_policy_change_candidate
