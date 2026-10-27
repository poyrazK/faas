from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_run_diagnostics_response_state_reason import (
    WorkflowRunDiagnosticsResponseStateReason,
    check_workflow_run_diagnostics_response_state_reason,
)
from ..models.workflow_run_diagnostics_response_status import (
    WorkflowRunDiagnosticsResponseStatus,
    check_workflow_run_diagnostics_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_diagnostic_step import WorkflowDiagnosticStep
    from ..models.workflow_resume_preview import WorkflowResumePreview


T = TypeVar("T", bound="WorkflowRunDiagnosticsResponse")


@_attrs_define
class WorkflowRunDiagnosticsResponse:
    """Read-only durable state and recovery preview. No execution or capacity reservation. Steps are bounded by existing
    workflow and iteration limits.

    """

    run_id: UUID
    workflow_name: str
    status: WorkflowRunDiagnosticsResponseStatus
    observed_at: datetime.datetime
    legacy_unpinned: bool
    state_reason: WorkflowRunDiagnosticsResponseStateReason
    """Durable state or dispatch admission reason at observation. Ready does not imply immediate execution or
    runtime availability. Inspect step kinds for parked wait details."""
    due_age_seconds: float
    """Age since eligibility including capacity-blocked work and expired leases; zero for future waits and live
    claims."""
    stale_lease: bool
    steps: list[WorkflowDiagnosticStep]
    resume: WorkflowResumePreview
    """A continuation plan based on the current run generation and recovery admission checks."""
    deployment_id: UUID | Unset = UNSET
    """Original code pin retained across continuation. Omitted for legacy unpinned runs."""
    next_wake_at: datetime.datetime | Unset = UNSET
    """Next intentional wake or scheduling deadline when it is in the future."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        run_id = str(self.run_id)

        workflow_name = self.workflow_name

        status: str = self.status

        observed_at = self.observed_at.isoformat()

        legacy_unpinned = self.legacy_unpinned

        state_reason: str = self.state_reason

        due_age_seconds = self.due_age_seconds

        stale_lease = self.stale_lease

        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        resume = self.resume.to_dict()

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        next_wake_at: str | Unset = UNSET
        if not isinstance(self.next_wake_at, Unset):
            next_wake_at = self.next_wake_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "run_id": run_id,
                "workflow_name": workflow_name,
                "status": status,
                "observed_at": observed_at,
                "legacy_unpinned": legacy_unpinned,
                "state_reason": state_reason,
                "due_age_seconds": due_age_seconds,
                "stale_lease": stale_lease,
                "steps": steps,
                "resume": resume,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if next_wake_at is not UNSET:
            field_dict["next_wake_at"] = next_wake_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_diagnostic_step import WorkflowDiagnosticStep
        from ..models.workflow_resume_preview import WorkflowResumePreview

        d = dict(src_dict)
        run_id = UUID(d.pop("run_id"))

        workflow_name = d.pop("workflow_name")

        status = check_workflow_run_diagnostics_response_status(d.pop("status"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        legacy_unpinned = d.pop("legacy_unpinned")

        state_reason = check_workflow_run_diagnostics_response_state_reason(d.pop("state_reason"))

        due_age_seconds = d.pop("due_age_seconds")

        stale_lease = d.pop("stale_lease")

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = WorkflowDiagnosticStep.from_dict(steps_item_data)

            steps.append(steps_item)

        resume = WorkflowResumePreview.from_dict(d.pop("resume"))

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        _next_wake_at = d.pop("next_wake_at", UNSET)
        next_wake_at: datetime.datetime | Unset
        if isinstance(_next_wake_at, Unset):
            next_wake_at = UNSET
        else:
            next_wake_at = datetime.datetime.fromisoformat(_next_wake_at)

        workflow_run_diagnostics_response = cls(
            run_id=run_id,
            workflow_name=workflow_name,
            status=status,
            observed_at=observed_at,
            legacy_unpinned=legacy_unpinned,
            state_reason=state_reason,
            due_age_seconds=due_age_seconds,
            stale_lease=stale_lease,
            steps=steps,
            resume=resume,
            deployment_id=deployment_id,
            next_wake_at=next_wake_at,
        )

        workflow_run_diagnostics_response.additional_properties = d
        return workflow_run_diagnostics_response

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
