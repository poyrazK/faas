from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_recovery_inspection_execution_kind import (
    OperationRecoveryInspectionExecutionKind,
    check_operation_recovery_inspection_execution_kind,
)
from ..models.operation_recovery_inspection_state import (
    OperationRecoveryInspectionState,
    check_operation_recovery_inspection_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_recovery_artifact import OperationRecoveryArtifact
    from ..models.operation_recovery_step import OperationRecoveryStep


T = TypeVar("T", bound="OperationRecoveryInspection")


@_attrs_define
class OperationRecoveryInspection:
    """Account-only durable recovery evidence with execution secrets and payloads omitted."""

    operation_id: UUID
    generation: int
    state: OperationRecoveryInspectionState
    cancellation_requested: bool
    execution_kind: OperationRecoveryInspectionExecutionKind
    execution_state: str
    attempt: int
    deployment_id: UUID
    steps: list[OperationRecoveryStep]
    artifacts: list[OperationRecoveryArtifact]
    retry_blockers: list[str]
    inspection_revision: str
    """Digest of durable execution evidence and file bindings; excludes observation time and independent
    notification state."""
    observed_at: datetime.datetime
    failure_code: str | Unset = UNSET
    invocation_id: UUID | Unset = UNSET
    job_run_id: UUID | Unset = UNSET
    workflow_run_id: UUID | Unset = UNSET
    release_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        generation = self.generation

        state: str = self.state

        cancellation_requested = self.cancellation_requested

        execution_kind: str = self.execution_kind

        execution_state = self.execution_state

        attempt = self.attempt

        deployment_id = str(self.deployment_id)

        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        artifacts = []
        for artifacts_item_data in self.artifacts:
            artifacts_item = artifacts_item_data.to_dict()
            artifacts.append(artifacts_item)

        retry_blockers = self.retry_blockers

        inspection_revision = self.inspection_revision

        observed_at = self.observed_at.isoformat()

        failure_code = self.failure_code

        invocation_id: str | Unset = UNSET
        if not isinstance(self.invocation_id, Unset):
            invocation_id = str(self.invocation_id)

        job_run_id: str | Unset = UNSET
        if not isinstance(self.job_run_id, Unset):
            job_run_id = str(self.job_run_id)

        workflow_run_id: str | Unset = UNSET
        if not isinstance(self.workflow_run_id, Unset):
            workflow_run_id = str(self.workflow_run_id)

        release_id: str | Unset = UNSET
        if not isinstance(self.release_id, Unset):
            release_id = str(self.release_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "operation_id": operation_id,
                "generation": generation,
                "state": state,
                "cancellation_requested": cancellation_requested,
                "execution_kind": execution_kind,
                "execution_state": execution_state,
                "attempt": attempt,
                "deployment_id": deployment_id,
                "steps": steps,
                "artifacts": artifacts,
                "retry_blockers": retry_blockers,
                "inspection_revision": inspection_revision,
                "observed_at": observed_at,
            }
        )
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if invocation_id is not UNSET:
            field_dict["invocation_id"] = invocation_id
        if job_run_id is not UNSET:
            field_dict["job_run_id"] = job_run_id
        if workflow_run_id is not UNSET:
            field_dict["workflow_run_id"] = workflow_run_id
        if release_id is not UNSET:
            field_dict["release_id"] = release_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_recovery_artifact import OperationRecoveryArtifact
        from ..models.operation_recovery_step import OperationRecoveryStep

        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        generation = d.pop("generation")

        state = check_operation_recovery_inspection_state(d.pop("state"))

        cancellation_requested = d.pop("cancellation_requested")

        execution_kind = check_operation_recovery_inspection_execution_kind(d.pop("execution_kind"))

        execution_state = d.pop("execution_state")

        attempt = d.pop("attempt")

        deployment_id = UUID(d.pop("deployment_id"))

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = OperationRecoveryStep.from_dict(steps_item_data)

            steps.append(steps_item)

        artifacts = []
        _artifacts = d.pop("artifacts")
        for artifacts_item_data in _artifacts:
            artifacts_item = OperationRecoveryArtifact.from_dict(artifacts_item_data)

            artifacts.append(artifacts_item)

        retry_blockers = cast(list[str], d.pop("retry_blockers"))

        inspection_revision = d.pop("inspection_revision")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        failure_code = d.pop("failure_code", UNSET)

        _invocation_id = d.pop("invocation_id", UNSET)
        invocation_id: UUID | Unset
        if isinstance(_invocation_id, Unset):
            invocation_id = UNSET
        else:
            invocation_id = UUID(_invocation_id)

        _job_run_id = d.pop("job_run_id", UNSET)
        job_run_id: UUID | Unset
        if isinstance(_job_run_id, Unset):
            job_run_id = UNSET
        else:
            job_run_id = UUID(_job_run_id)

        _workflow_run_id = d.pop("workflow_run_id", UNSET)
        workflow_run_id: UUID | Unset
        if isinstance(_workflow_run_id, Unset):
            workflow_run_id = UNSET
        else:
            workflow_run_id = UUID(_workflow_run_id)

        _release_id = d.pop("release_id", UNSET)
        release_id: UUID | Unset
        if isinstance(_release_id, Unset):
            release_id = UNSET
        else:
            release_id = UUID(_release_id)

        operation_recovery_inspection = cls(
            operation_id=operation_id,
            generation=generation,
            state=state,
            cancellation_requested=cancellation_requested,
            execution_kind=execution_kind,
            execution_state=execution_state,
            attempt=attempt,
            deployment_id=deployment_id,
            steps=steps,
            artifacts=artifacts,
            retry_blockers=retry_blockers,
            inspection_revision=inspection_revision,
            observed_at=observed_at,
            failure_code=failure_code,
            invocation_id=invocation_id,
            job_run_id=job_run_id,
            workflow_run_id=workflow_run_id,
            release_id=release_id,
        )

        return operation_recovery_inspection
