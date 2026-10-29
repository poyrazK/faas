from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_outcome_link_status import (
    OperationWorkflowOutcomeLinkStatus,
    check_operation_workflow_outcome_link_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowOutcomeLink")


@_attrs_define
class OperationWorkflowOutcomeLink:
    """Observed report-to-outcome path on the same retained history page. Links reported sequence without establishing
    business causation; durations are meaningful only for linked status.

    """

    status: OperationWorkflowOutcomeLinkStatus
    summary: str
    elapsed_seconds: int
    """Observed duration from this report to the linked outcome. Meaningful only when status is linked."""
    blocked_seconds: int
    """Observed intervals with at least one reported blocker. Overlapping blockers count once. Meaningful only when
    status is linked."""
    outcome_report_id: UUID | Unset = UNSET
    outcome_operation_id: UUID | Unset = UNSET
    outcome_revision: int | Unset = UNSET
    outcome_code: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        summary = self.summary

        elapsed_seconds = self.elapsed_seconds

        blocked_seconds = self.blocked_seconds

        outcome_report_id: str | Unset = UNSET
        if not isinstance(self.outcome_report_id, Unset):
            outcome_report_id = str(self.outcome_report_id)

        outcome_operation_id: str | Unset = UNSET
        if not isinstance(self.outcome_operation_id, Unset):
            outcome_operation_id = str(self.outcome_operation_id)

        outcome_revision = self.outcome_revision

        outcome_code = self.outcome_code

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
                "summary": summary,
                "elapsed_seconds": elapsed_seconds,
                "blocked_seconds": blocked_seconds,
            }
        )
        if outcome_report_id is not UNSET:
            field_dict["outcome_report_id"] = outcome_report_id
        if outcome_operation_id is not UNSET:
            field_dict["outcome_operation_id"] = outcome_operation_id
        if outcome_revision is not UNSET:
            field_dict["outcome_revision"] = outcome_revision
        if outcome_code is not UNSET:
            field_dict["outcome_code"] = outcome_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_operation_workflow_outcome_link_status(d.pop("status"))

        summary = d.pop("summary")

        elapsed_seconds = d.pop("elapsed_seconds")

        blocked_seconds = d.pop("blocked_seconds")

        _outcome_report_id = d.pop("outcome_report_id", UNSET)
        outcome_report_id: UUID | Unset
        if isinstance(_outcome_report_id, Unset):
            outcome_report_id = UNSET
        else:
            outcome_report_id = UUID(_outcome_report_id)

        _outcome_operation_id = d.pop("outcome_operation_id", UNSET)
        outcome_operation_id: UUID | Unset
        if isinstance(_outcome_operation_id, Unset):
            outcome_operation_id = UNSET
        else:
            outcome_operation_id = UUID(_outcome_operation_id)

        outcome_revision = d.pop("outcome_revision", UNSET)

        outcome_code = d.pop("outcome_code", UNSET)

        operation_workflow_outcome_link = cls(
            status=status,
            summary=summary,
            elapsed_seconds=elapsed_seconds,
            blocked_seconds=blocked_seconds,
            outcome_report_id=outcome_report_id,
            outcome_operation_id=outcome_operation_id,
            outcome_revision=outcome_revision,
            outcome_code=outcome_code,
        )

        return operation_workflow_outcome_link
