from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_gap_progress_group_gap_status import (
    OperationWorkflowGapProgressGroupGapStatus,
    check_operation_workflow_gap_progress_group_gap_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_duration_distribution import OperationWorkflowDurationDistribution


T = TypeVar("T", bound="OperationWorkflowGapProgressGroup")


@_attrs_define
class OperationWorkflowGapProgressGroup:
    """Current gap group ordered by contract, state, owner and category, before condition previews. Each workflow
    contributes once per group. Blocked duration includes observed report intervals across permitted contract boundaries
    and does not measure gap age or business severity.

    """

    contract_version: int
    state: str
    unassigned: bool
    gap_status: OperationWorkflowGapProgressGroupGapStatus
    missing_condition_count: int
    blocked_time: OperationWorkflowDurationDistribution
    """One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is
    zero."""
    owner: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        contract_version = self.contract_version

        state = self.state

        unassigned = self.unassigned

        gap_status: str = self.gap_status

        missing_condition_count = self.missing_condition_count

        blocked_time = self.blocked_time.to_dict()

        owner = self.owner

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "contract_version": contract_version,
                "state": state,
                "unassigned": unassigned,
                "gap_status": gap_status,
                "missing_condition_count": missing_condition_count,
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
        contract_version = d.pop("contract_version")

        state = d.pop("state")

        unassigned = d.pop("unassigned")

        gap_status = check_operation_workflow_gap_progress_group_gap_status(d.pop("gap_status"))

        missing_condition_count = d.pop("missing_condition_count")

        blocked_time = OperationWorkflowDurationDistribution.from_dict(d.pop("blocked_time"))

        owner = d.pop("owner", UNSET)

        operation_workflow_gap_progress_group = cls(
            contract_version=contract_version,
            state=state,
            unassigned=unassigned,
            gap_status=gap_status,
            missing_condition_count=missing_condition_count,
            blocked_time=blocked_time,
            owner=owner,
        )

        return operation_workflow_gap_progress_group
