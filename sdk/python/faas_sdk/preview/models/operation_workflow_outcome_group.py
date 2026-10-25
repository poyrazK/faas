from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowOutcomeGroup")


@_attrs_define
class OperationWorkflowOutcomeGroup:
    """Count of terminal workflow instances matching one outcome aggregation value."""

    value: str
    workflow_count: int

    def to_dict(self) -> dict[str, Any]:
        value = self.value

        workflow_count = self.workflow_count

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "value": value,
                "workflow_count": workflow_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        value = d.pop("value")

        workflow_count = d.pop("workflow_count")

        operation_workflow_outcome_group = cls(
            value=value,
            workflow_count=workflow_count,
        )

        return operation_workflow_outcome_group
