from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowDurationDistribution")


@_attrs_define
class OperationWorkflowDurationDistribution:
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is zero."""

    workflow_count: int
    total_seconds: int
    p50_seconds: int
    p95_seconds: int

    def to_dict(self) -> dict[str, Any]:
        workflow_count = self.workflow_count

        total_seconds = self.total_seconds

        p50_seconds = self.p50_seconds

        p95_seconds = self.p95_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow_count": workflow_count,
                "total_seconds": total_seconds,
                "p50_seconds": p50_seconds,
                "p95_seconds": p95_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workflow_count = d.pop("workflow_count")

        total_seconds = d.pop("total_seconds")

        p50_seconds = d.pop("p50_seconds")

        p95_seconds = d.pop("p95_seconds")

        operation_workflow_duration_distribution = cls(
            workflow_count=workflow_count,
            total_seconds=total_seconds,
            p50_seconds=p50_seconds,
            p95_seconds=p95_seconds,
        )

        return operation_workflow_duration_distribution
