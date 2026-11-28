from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_retained_milestone_requirement import OperationWorkflowRetainedMilestoneRequirement


T = TypeVar("T", bound="OperationWorkflowRetainedMilestoneFreshnessRule")


@_attrs_define
class OperationWorkflowRetainedMilestoneFreshnessRule:
    """Versioned maximum age for one declared mandatory or alternative retained prerequisite; measured from business
    occurrence time.

    """

    prerequisite: OperationWorkflowRetainedMilestoneRequirement
    """Source operation and milestone bound to a declared transaction-backed step of the selected workflow
    contract."""
    max_age_seconds: int

    def to_dict(self) -> dict[str, Any]:
        prerequisite = self.prerequisite.to_dict()

        max_age_seconds = self.max_age_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "prerequisite": prerequisite,
                "max_age_seconds": max_age_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_retained_milestone_requirement import (
            OperationWorkflowRetainedMilestoneRequirement,
        )

        d = dict(src_dict)
        prerequisite = OperationWorkflowRetainedMilestoneRequirement.from_dict(d.pop("prerequisite"))

        max_age_seconds = d.pop("max_age_seconds")

        operation_workflow_retained_milestone_freshness_rule = cls(
            prerequisite=prerequisite,
            max_age_seconds=max_age_seconds,
        )

        return operation_workflow_retained_milestone_freshness_rule
