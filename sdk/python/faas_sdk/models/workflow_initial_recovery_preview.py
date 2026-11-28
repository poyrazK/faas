from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_initial_failure import WorkflowInitialFailure
    from ..models.workflow_run_recovery_target import WorkflowRunRecoveryTarget


T = TypeVar("T", bound="WorkflowInitialRecoveryPreview")


@_attrs_define
class WorkflowInitialRecoveryPreview:
    """Safety preview for creating the first recovery job from a captured failure."""

    failure: WorkflowInitialFailure
    """One captured generation-zero workflow failure."""
    observed_at: datetime.datetime
    reason: str
    """Stable diagnostic reason such as eligible, changed, run_not_retained, capacity or resume_ineligible."""
    blockers: list[str]
    target: WorkflowRunRecoveryTarget | Unset = UNSET
    """One reviewed workflow run and resume count. Retry children identify the saved parent item. Initial failure
    targets may identify retained generation-zero evidence, which is checked again at creation and admission."""

    def to_dict(self) -> dict[str, Any]:
        failure = self.failure.to_dict()

        observed_at = self.observed_at.isoformat()

        reason = self.reason

        blockers = self.blockers

        target: dict[str, Any] | Unset = UNSET
        if not isinstance(self.target, Unset):
            target = self.target.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "failure": failure,
                "observed_at": observed_at,
                "reason": reason,
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

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        reason = d.pop("reason")

        blockers = cast(list[str], d.pop("blockers"))

        _target = d.pop("target", UNSET)
        target: WorkflowRunRecoveryTarget | Unset
        if isinstance(_target, Unset):
            target = UNSET
        else:
            target = WorkflowRunRecoveryTarget.from_dict(_target)

        workflow_initial_recovery_preview = cls(
            failure=failure,
            observed_at=observed_at,
            reason=reason,
            blockers=blockers,
            target=target,
        )

        return workflow_initial_recovery_preview
