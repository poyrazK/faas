from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowPlaybookStepDependency")


@_attrs_define
class OperationWorkflowPlaybookStepDependency:
    """OperationWorkflowPlaybookStepDependency. One step waits for all named prerequisite reviews in the same pinned
    outcome checklist; unknown labels, self-edges, duplicates and cycles are rejected.

    """

    label: str
    """OperationWorkflowPlaybookStepDependency.label. Exact declared dependent step label, unique within the graph."""
    prerequisites: list[str]
    """OperationWorkflowPlaybookStepDependency.prerequisites. Exact declared prerequisites; each must be reviewed
    complete with its own prerequisites satisfied."""

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        prerequisites = self.prerequisites

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "label": label,
                "prerequisites": prerequisites,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        label = d.pop("label")

        prerequisites = cast(list[str], d.pop("prerequisites"))

        operation_workflow_playbook_step_dependency = cls(
            label=label,
            prerequisites=prerequisites,
        )

        return operation_workflow_playbook_step_dependency
