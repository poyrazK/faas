from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_review_impact_kind import (
    OperationWorkflowPlaybookReviewImpactKind,
    check_operation_workflow_playbook_review_impact_kind,
)
from ..models.operation_workflow_playbook_review_impact_priority import (
    OperationWorkflowPlaybookReviewImpactPriority,
    check_operation_workflow_playbook_review_impact_priority,
)

T = TypeVar("T", bound="OperationWorkflowPlaybookReviewImpact")


@_attrs_define
class OperationWorkflowPlaybookReviewImpact:
    """OperationWorkflowPlaybookReviewImpact. Frozen application guidance explaining the business result protected by human
    review; advisory confidence, independent of reported business state.

    """

    kind: OperationWorkflowPlaybookReviewImpactKind
    """OperationWorkflowPlaybookReviewImpact.kind. Result category in this workflow case."""
    code: str
    """OperationWorkflowPlaybookReviewImpact.code. Milestone slug or expected or explicitly acceptable outcome code
    from this pinned playbook. A milestone name is declared guidance, not verified existence or completion."""
    description: str
    """OperationWorkflowPlaybookReviewImpact.description. Application-authored explanation of the business risk;
    trimmed control-free UTF-8 text limited to 1024 bytes."""
    priority: OperationWorkflowPlaybookReviewImpactPriority
    """OperationWorkflowPlaybookReviewImpact.priority. Declared priority for operator triage, independent of
    deadline and observed business success."""

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        code = self.code

        description = self.description

        priority: str = self.priority

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "code": code,
                "description": description,
                "priority": priority,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_operation_workflow_playbook_review_impact_kind(d.pop("kind"))

        code = d.pop("code")

        description = d.pop("description")

        priority = check_operation_workflow_playbook_review_impact_priority(d.pop("priority"))

        operation_workflow_playbook_review_impact = cls(
            kind=kind,
            code=code,
            description=description,
            priority=priority,
        )

        return operation_workflow_playbook_review_impact
