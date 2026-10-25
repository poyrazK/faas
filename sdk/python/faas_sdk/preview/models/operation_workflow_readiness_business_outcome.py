from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_business_outcome_status import (
    OperationWorkflowReadinessBusinessOutcomeStatus,
    check_operation_workflow_readiness_business_outcome_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReadinessBusinessOutcome")


@_attrs_define
class OperationWorkflowReadinessBusinessOutcome:
    """Advisory comparison of the expected terminal outcome with a scoped retained application report. Missing state, stale
    reports, contract changes, older revisions and terminal reports without outcome codes remain unknown. Explicit
    outcome codes do not imply causation or authorize execution. Completion snapshots are immutable; current checks are
    live reads.

    """

    expected_outcome_code: str
    """OperationWorkflowReadinessBusinessOutcome.expected_outcome_code. Scoped application-reported business result
    relative to the caller-selected expected terminal outcome; independent of coordination completion and readiness.
   """
    checked_at: datetime.datetime
    """OperationWorkflowReadinessBusinessOutcome.checked_at. Scoped application-reported business result relative
    to the caller-selected expected terminal outcome; independent of coordination completion and readiness."""
    status: OperationWorkflowReadinessBusinessOutcomeStatus
    """OperationWorkflowReadinessBusinessOutcome.status. Scoped application-reported business result relative to
    the caller-selected expected terminal outcome; independent of coordination completion and readiness."""
    explanation: str
    """OperationWorkflowReadinessBusinessOutcome.explanation. Scoped application-reported business result relative
    to the caller-selected expected terminal outcome; independent of coordination completion and readiness."""
    reporting_operation_id: UUID | Unset = UNSET
    """OperationWorkflowReadinessBusinessOutcome.reporting_operation_id. Scoped application-reported business
    result relative to the caller-selected expected terminal outcome; independent of coordination completion and
    readiness."""
    report_id: UUID | Unset = UNSET
    """OperationWorkflowReadinessBusinessOutcome.report_id. Scoped application-reported business result relative to
    the caller-selected expected terminal outcome; independent of coordination completion and readiness."""
    state_revision: int | Unset = UNSET
    """OperationWorkflowReadinessBusinessOutcome.state_revision. Scoped application-reported business result
    relative to the caller-selected expected terminal outcome; independent of coordination completion and readiness.
   """
    contract_version: int | Unset = UNSET
    """OperationWorkflowReadinessBusinessOutcome.contract_version. Scoped application-reported business result
    relative to the caller-selected expected terminal outcome; independent of coordination completion and readiness.
   """
    state: str | Unset = UNSET
    """OperationWorkflowReadinessBusinessOutcome.state. Scoped application-reported business result relative to the
    caller-selected expected terminal outcome; independent of coordination completion and readiness."""
    reported_outcome_code: str | Unset = UNSET
    """OperationWorkflowReadinessBusinessOutcome.reported_outcome_code. Scoped application-reported business result
    relative to the caller-selected expected terminal outcome; independent of coordination completion and readiness.
   """
    reported_outcome_description: str | Unset = UNSET
    """OperationWorkflowReadinessBusinessOutcome.reported_outcome_description. Scoped application-reported business
    result relative to the caller-selected expected terminal outcome; independent of coordination completion and
    readiness."""
    occurred_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReadinessBusinessOutcome.occurred_at. Scoped application-reported business result relative
    to the caller-selected expected terminal outcome; independent of coordination completion and readiness."""

    def to_dict(self) -> dict[str, Any]:
        expected_outcome_code = self.expected_outcome_code

        checked_at = self.checked_at.isoformat()

        status: str = self.status

        explanation = self.explanation

        reporting_operation_id: str | Unset = UNSET
        if not isinstance(self.reporting_operation_id, Unset):
            reporting_operation_id = str(self.reporting_operation_id)

        report_id: str | Unset = UNSET
        if not isinstance(self.report_id, Unset):
            report_id = str(self.report_id)

        state_revision = self.state_revision

        contract_version = self.contract_version

        state = self.state

        reported_outcome_code = self.reported_outcome_code

        reported_outcome_description = self.reported_outcome_description

        occurred_at: str | Unset = UNSET
        if not isinstance(self.occurred_at, Unset):
            occurred_at = self.occurred_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_outcome_code": expected_outcome_code,
                "checked_at": checked_at,
                "status": status,
                "explanation": explanation,
            }
        )
        if reporting_operation_id is not UNSET:
            field_dict["reporting_operation_id"] = reporting_operation_id
        if report_id is not UNSET:
            field_dict["report_id"] = report_id
        if state_revision is not UNSET:
            field_dict["state_revision"] = state_revision
        if contract_version is not UNSET:
            field_dict["contract_version"] = contract_version
        if state is not UNSET:
            field_dict["state"] = state
        if reported_outcome_code is not UNSET:
            field_dict["reported_outcome_code"] = reported_outcome_code
        if reported_outcome_description is not UNSET:
            field_dict["reported_outcome_description"] = reported_outcome_description
        if occurred_at is not UNSET:
            field_dict["occurred_at"] = occurred_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_outcome_code = d.pop("expected_outcome_code")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        status = check_operation_workflow_readiness_business_outcome_status(d.pop("status"))

        explanation = d.pop("explanation")

        _reporting_operation_id = d.pop("reporting_operation_id", UNSET)
        reporting_operation_id: UUID | Unset
        if isinstance(_reporting_operation_id, Unset):
            reporting_operation_id = UNSET
        else:
            reporting_operation_id = UUID(_reporting_operation_id)

        _report_id = d.pop("report_id", UNSET)
        report_id: UUID | Unset
        if isinstance(_report_id, Unset):
            report_id = UNSET
        else:
            report_id = UUID(_report_id)

        state_revision = d.pop("state_revision", UNSET)

        contract_version = d.pop("contract_version", UNSET)

        state = d.pop("state", UNSET)

        reported_outcome_code = d.pop("reported_outcome_code", UNSET)

        reported_outcome_description = d.pop("reported_outcome_description", UNSET)

        _occurred_at = d.pop("occurred_at", UNSET)
        occurred_at: datetime.datetime | Unset
        if isinstance(_occurred_at, Unset):
            occurred_at = UNSET
        else:
            occurred_at = datetime.datetime.fromisoformat(_occurred_at)

        operation_workflow_readiness_business_outcome = cls(
            expected_outcome_code=expected_outcome_code,
            checked_at=checked_at,
            status=status,
            explanation=explanation,
            reporting_operation_id=reporting_operation_id,
            report_id=report_id,
            state_revision=state_revision,
            contract_version=contract_version,
            state=state,
            reported_outcome_code=reported_outcome_code,
            reported_outcome_description=reported_outcome_description,
            occurred_at=occurred_at,
        )

        return operation_workflow_readiness_business_outcome
