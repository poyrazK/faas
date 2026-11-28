from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution


T = TypeVar("T", bound="OperationWorkflowStatePerformance")


@_attrs_define
class OperationWorkflowStatePerformance:
    """Cohort duration distribution and evaluated SLA visits for one state and contract version."""

    sla_evaluated_visit_count: int
    sla_breached_visit_count: int
    contract_version: int
    state: str
    duration: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""

    def to_dict(self) -> dict[str, Any]:
        sla_evaluated_visit_count = self.sla_evaluated_visit_count

        sla_breached_visit_count = self.sla_breached_visit_count

        contract_version = self.contract_version

        state = self.state

        duration = self.duration.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "sla_evaluated_visit_count": sla_evaluated_visit_count,
                "sla_breached_visit_count": sla_breached_visit_count,
                "contract_version": contract_version,
                "state": state,
                "duration": duration,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution

        d = dict(src_dict)
        sla_evaluated_visit_count = d.pop("sla_evaluated_visit_count")

        sla_breached_visit_count = d.pop("sla_breached_visit_count")

        contract_version = d.pop("contract_version")

        state = d.pop("state")

        duration = OperationWorkflowDurationDistribution.from_dict(d.pop("duration"))

        operation_workflow_state_performance = cls(
            sla_evaluated_visit_count=sla_evaluated_visit_count,
            sla_breached_visit_count=sla_breached_visit_count,
            contract_version=contract_version,
            state=state,
            duration=duration,
        )

        return operation_workflow_state_performance
