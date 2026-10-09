from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_recovery_decision_resolution import (
    OperationRecoveryDecisionResolution,
    check_operation_recovery_decision_resolution,
)
from ..models.operation_recovery_decision_state import (
    OperationRecoveryDecisionState,
    check_operation_recovery_decision_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationRecoveryDecision")


@_attrs_define
class OperationRecoveryDecision:
    """Immutable historical recovery acknowledgement; excludes raw evidence, result and credentials."""

    operation_id: UUID
    recovery_id: str
    request_fingerprint: str
    expected_generation: int
    generation: int
    resolution: OperationRecoveryDecisionResolution
    state: OperationRecoveryDecisionState
    recorded_at: datetime.datetime
    expires_at: datetime.datetime
    expected_inspection_revision: str | Unset = UNSET
    invocation_id: UUID | Unset = UNSET
    job_run_id: UUID | Unset = UNSET
    workflow_run_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        recovery_id = self.recovery_id

        request_fingerprint = self.request_fingerprint

        expected_generation = self.expected_generation

        generation = self.generation

        resolution: str = self.resolution

        state: str = self.state

        recorded_at = self.recorded_at.isoformat()

        expires_at = self.expires_at.isoformat()

        expected_inspection_revision = self.expected_inspection_revision

        invocation_id: str | Unset = UNSET
        if not isinstance(self.invocation_id, Unset):
            invocation_id = str(self.invocation_id)

        job_run_id: str | Unset = UNSET
        if not isinstance(self.job_run_id, Unset):
            job_run_id = str(self.job_run_id)

        workflow_run_id: str | Unset = UNSET
        if not isinstance(self.workflow_run_id, Unset):
            workflow_run_id = str(self.workflow_run_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "operation_id": operation_id,
                "recovery_id": recovery_id,
                "request_fingerprint": request_fingerprint,
                "expected_generation": expected_generation,
                "generation": generation,
                "resolution": resolution,
                "state": state,
                "recorded_at": recorded_at,
                "expires_at": expires_at,
            }
        )
        if expected_inspection_revision is not UNSET:
            field_dict["expected_inspection_revision"] = expected_inspection_revision
        if invocation_id is not UNSET:
            field_dict["invocation_id"] = invocation_id
        if job_run_id is not UNSET:
            field_dict["job_run_id"] = job_run_id
        if workflow_run_id is not UNSET:
            field_dict["workflow_run_id"] = workflow_run_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        recovery_id = d.pop("recovery_id")

        request_fingerprint = d.pop("request_fingerprint")

        expected_generation = d.pop("expected_generation")

        generation = d.pop("generation")

        resolution = check_operation_recovery_decision_resolution(d.pop("resolution"))

        state = check_operation_recovery_decision_state(d.pop("state"))

        recorded_at = datetime.datetime.fromisoformat(d.pop("recorded_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        expected_inspection_revision = d.pop("expected_inspection_revision", UNSET)

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

        operation_recovery_decision = cls(
            operation_id=operation_id,
            recovery_id=recovery_id,
            request_fingerprint=request_fingerprint,
            expected_generation=expected_generation,
            generation=generation,
            resolution=resolution,
            state=state,
            recorded_at=recorded_at,
            expires_at=expires_at,
            expected_inspection_revision=expected_inspection_revision,
            invocation_id=invocation_id,
            job_run_id=job_run_id,
            workflow_run_id=workflow_run_id,
        )

        return operation_recovery_decision
