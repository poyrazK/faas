from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_step_readiness_status import (
    OperationWorkflowPlaybookStepReadinessStatus,
    check_operation_workflow_playbook_step_readiness_status,
)

T = TypeVar("T", bound="OperationWorkflowPlaybookStepReadiness")


@_attrs_define
class OperationWorkflowPlaybookStepReadiness:
    """OperationWorkflowPlaybookStepReadiness. Human review dependency status, not business state or execution authority."""

    status: OperationWorkflowPlaybookStepReadinessStatus
    """OperationWorkflowPlaybookStepReadiness.status. Whether all declared prerequisite reviews are complete and
    their dependency chains satisfied."""
    waiting_for: list[str]
    """OperationWorkflowPlaybookStepReadiness.waiting_for. Direct prerequisites currently missing complete and
    ready reviews; empty for ready steps."""
    review_suggested: bool
    """OperationWorkflowPlaybookStepReadiness.review_suggested. A previously completed dependent step now has
    unsatisfied prerequisites; original review is preserved."""

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        waiting_for = self.waiting_for

        review_suggested = self.review_suggested

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
                "waiting_for": waiting_for,
                "review_suggested": review_suggested,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_operation_workflow_playbook_step_readiness_status(d.pop("status"))

        waiting_for = cast(list[str], d.pop("waiting_for"))

        review_suggested = d.pop("review_suggested")

        operation_workflow_playbook_step_readiness = cls(
            status=status,
            waiting_for=waiting_for,
            review_suggested=review_suggested,
        )

        return operation_workflow_playbook_step_readiness
