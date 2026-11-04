from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_obligation_context_status import (
    OperationWorkflowObligationContextStatus,
    check_operation_workflow_obligation_context_status,
)

T = TypeVar("T", bound="OperationWorkflowObligationContext")


@_attrs_define
class OperationWorkflowObligationContext:
    """Fresh exact-case transition context shared by obligation assessments. Detailed declared requirements and dependency
    identities remain in instance.readiness, avoiding duplicated evidence payloads. Stale or absent reports never
    produce a positive transition verdict; a satisfied edge does not grant application execution or bind a free-form
    plan.

    """

    status: OperationWorkflowObligationContextStatus
    summary: str
    checked_at: datetime.datetime
    coverage_incomplete: bool
    transition_count: int
    evaluated_transition_count: int

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        summary = self.summary

        checked_at = self.checked_at.isoformat()

        coverage_incomplete = self.coverage_incomplete

        transition_count = self.transition_count

        evaluated_transition_count = self.evaluated_transition_count

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
                "summary": summary,
                "checked_at": checked_at,
                "coverage_incomplete": coverage_incomplete,
                "transition_count": transition_count,
                "evaluated_transition_count": evaluated_transition_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_operation_workflow_obligation_context_status(d.pop("status"))

        summary = d.pop("summary")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        coverage_incomplete = d.pop("coverage_incomplete")

        transition_count = d.pop("transition_count")

        evaluated_transition_count = d.pop("evaluated_transition_count")

        operation_workflow_obligation_context = cls(
            status=status,
            summary=summary,
            checked_at=checked_at,
            coverage_incomplete=coverage_incomplete,
            transition_count=transition_count,
            evaluated_transition_count=evaluated_transition_count,
        )

        return operation_workflow_obligation_context
