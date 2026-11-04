from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution


T = TypeVar("T", bound="OperationWorkflowConditionRefreshOwnerGroup")


@_attrs_define
class OperationWorkflowConditionRefreshOwnerGroup:
    """Eligible ongoing workflows with overdue condition reports assigned to one pinned refresh owner. Missing owners are
    unassigned. Each workflow counts once per owner even with multiple overdue conditions, and can appear under several
    owners. Durations cover the entire observed workflow, not time under the owner. Guidance never authorizes
    verification or execution.

    """

    unassigned: bool
    overdue_condition_count: int
    elapsed_time: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""
    blocked_time: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""
    owner: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        unassigned = self.unassigned

        overdue_condition_count = self.overdue_condition_count

        elapsed_time = self.elapsed_time.to_dict()

        blocked_time = self.blocked_time.to_dict()

        owner = self.owner

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "unassigned": unassigned,
                "overdue_condition_count": overdue_condition_count,
                "elapsed_time": elapsed_time,
                "blocked_time": blocked_time,
            }
        )
        if owner is not UNSET:
            field_dict["owner"] = owner

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution

        d = dict(src_dict)
        unassigned = d.pop("unassigned")

        overdue_condition_count = d.pop("overdue_condition_count")

        elapsed_time = OperationWorkflowDurationDistribution.from_dict(d.pop("elapsed_time"))

        blocked_time = OperationWorkflowDurationDistribution.from_dict(d.pop("blocked_time"))

        owner = d.pop("owner", UNSET)

        operation_workflow_condition_refresh_owner_group = cls(
            unassigned=unassigned,
            overdue_condition_count=overdue_condition_count,
            elapsed_time=elapsed_time,
            blocked_time=blocked_time,
            owner=owner,
        )

        return operation_workflow_condition_refresh_owner_group
