from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution


T = TypeVar("T", bound="OperationWorkflowBlockerPerformance")


@_attrs_define
class OperationWorkflowBlockerPerformance:
    """Cohort duration distribution for one blocker target, code, contract version and observed owner."""

    contract_version: int
    operation: str
    code: str
    owner: str
    """Application-reported blocker owner for this cohort group; empty means unassigned."""
    duration: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""

    def to_dict(self) -> dict[str, Any]:
        contract_version = self.contract_version

        operation = self.operation

        code = self.code

        owner = self.owner

        duration = self.duration.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "contract_version": contract_version,
                "operation": operation,
                "code": code,
                "owner": owner,
                "duration": duration,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution

        d = dict(src_dict)
        contract_version = d.pop("contract_version")

        operation = d.pop("operation")

        code = d.pop("code")

        owner = d.pop("owner")

        duration = OperationWorkflowDurationDistribution.from_dict(d.pop("duration"))

        operation_workflow_blocker_performance = cls(
            contract_version=contract_version,
            operation=operation,
            code=code,
            owner=owner,
            duration=duration,
        )

        return operation_workflow_blocker_performance
