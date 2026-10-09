from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution


T = TypeVar("T", bound="OperationWorkflowVerificationPerformance")


@_attrs_define
class OperationWorkflowVerificationPerformance:
    owner: str
    """Empty means unassigned."""
    pending_resolution_count: int
    duration: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""

    def to_dict(self) -> dict[str, Any]:
        owner = self.owner

        pending_resolution_count = self.pending_resolution_count

        duration = self.duration.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "owner": owner,
                "pending_resolution_count": pending_resolution_count,
                "duration": duration,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution

        d = dict(src_dict)
        owner = d.pop("owner")

        pending_resolution_count = d.pop("pending_resolution_count")

        duration = OperationWorkflowDurationDistribution.from_dict(d.pop("duration"))

        operation_workflow_verification_performance = cls(
            owner=owner,
            pending_resolution_count=pending_resolution_count,
            duration=duration,
        )

        return operation_workflow_verification_performance
