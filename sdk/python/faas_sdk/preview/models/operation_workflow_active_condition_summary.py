from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_active_condition_group import OperationWorkflowActiveConditionGroup
    from ..models.operation_workflow_condition_refresh_owner_group import OperationWorkflowConditionRefreshOwnerGroup


T = TypeVar("T", bound="OperationWorkflowActiveConditionSummary")


@_attrs_define
class OperationWorkflowActiveConditionSummary:
    """Latest explicit condition snapshots in the ongoing sample with complete retained history. The latest reason and
    latest nonempty selected-path snapshot provide independent contexts; a path without a fact clears that path context.
    Metadata-only reports preserve evidence. Explicit decision omission removes older conditions. Contract changes reset
    contexts. Conflicting results for an exact identity exclude the workflow. Missing conditions remain unreported. At
    most 256 contexts and 256 final condition identities per workflow qualify. Groups overlap; no present business truth
    or causation is inferred.

    """

    overdue_workflow_count: int
    """Eligible sampled workflows with at least one overdue selected condition report; counted once before owner
    display limits."""
    overdue_condition_count: int
    """Exact selected overdue condition identities across eligible sampled workflows before owner display limits."""
    refresh_owners_truncated: bool
    refresh_owners: list[OperationWorkflowConditionRefreshOwnerGroup]
    """First 64 owner strings in lexical order; unassigned sorts first. Counts for returned owners are exact.
    Workflows may overlap across owners."""
    matching_workflow_count: int
    sampled_workflow_count: int
    linked_workflow_count: int
    excluded_incomplete_workflow_count: int
    excluded_ambiguous_workflow_count: int
    excluded_condition_evidence_workflow_count: int
    unclassified_workflow_count: int
    cohort_truncated: bool
    groups_truncated: bool
    groups: list[OperationWorkflowActiveConditionGroup]
    """First 64 contract/decision/rule/version/condition ID/description/result identities in lexical order with
    exact counts for returned groups."""

    def to_dict(self) -> dict[str, Any]:
        overdue_workflow_count = self.overdue_workflow_count

        overdue_condition_count = self.overdue_condition_count

        refresh_owners_truncated = self.refresh_owners_truncated

        refresh_owners = []
        for refresh_owners_item_data in self.refresh_owners:
            refresh_owners_item = refresh_owners_item_data.to_dict()
            refresh_owners.append(refresh_owners_item)

        matching_workflow_count = self.matching_workflow_count

        sampled_workflow_count = self.sampled_workflow_count

        linked_workflow_count = self.linked_workflow_count

        excluded_incomplete_workflow_count = self.excluded_incomplete_workflow_count

        excluded_ambiguous_workflow_count = self.excluded_ambiguous_workflow_count

        excluded_condition_evidence_workflow_count = self.excluded_condition_evidence_workflow_count

        unclassified_workflow_count = self.unclassified_workflow_count

        cohort_truncated = self.cohort_truncated

        groups_truncated = self.groups_truncated

        groups = []
        for groups_item_data in self.groups:
            groups_item = groups_item_data.to_dict()
            groups.append(groups_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "overdue_workflow_count": overdue_workflow_count,
                "overdue_condition_count": overdue_condition_count,
                "refresh_owners_truncated": refresh_owners_truncated,
                "refresh_owners": refresh_owners,
                "matching_workflow_count": matching_workflow_count,
                "sampled_workflow_count": sampled_workflow_count,
                "linked_workflow_count": linked_workflow_count,
                "excluded_incomplete_workflow_count": excluded_incomplete_workflow_count,
                "excluded_ambiguous_workflow_count": excluded_ambiguous_workflow_count,
                "excluded_condition_evidence_workflow_count": excluded_condition_evidence_workflow_count,
                "unclassified_workflow_count": unclassified_workflow_count,
                "cohort_truncated": cohort_truncated,
                "groups_truncated": groups_truncated,
                "groups": groups,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_active_condition_group import OperationWorkflowActiveConditionGroup
        from ..models.operation_workflow_condition_refresh_owner_group import (
            OperationWorkflowConditionRefreshOwnerGroup,
        )

        d = dict(src_dict)
        overdue_workflow_count = d.pop("overdue_workflow_count")

        overdue_condition_count = d.pop("overdue_condition_count")

        refresh_owners_truncated = d.pop("refresh_owners_truncated")

        refresh_owners = []
        _refresh_owners = d.pop("refresh_owners")
        for refresh_owners_item_data in _refresh_owners:
            refresh_owners_item = OperationWorkflowConditionRefreshOwnerGroup.from_dict(refresh_owners_item_data)

            refresh_owners.append(refresh_owners_item)

        matching_workflow_count = d.pop("matching_workflow_count")

        sampled_workflow_count = d.pop("sampled_workflow_count")

        linked_workflow_count = d.pop("linked_workflow_count")

        excluded_incomplete_workflow_count = d.pop("excluded_incomplete_workflow_count")

        excluded_ambiguous_workflow_count = d.pop("excluded_ambiguous_workflow_count")

        excluded_condition_evidence_workflow_count = d.pop("excluded_condition_evidence_workflow_count")

        unclassified_workflow_count = d.pop("unclassified_workflow_count")

        cohort_truncated = d.pop("cohort_truncated")

        groups_truncated = d.pop("groups_truncated")

        groups = []
        _groups = d.pop("groups")
        for groups_item_data in _groups:
            groups_item = OperationWorkflowActiveConditionGroup.from_dict(groups_item_data)

            groups.append(groups_item)

        operation_workflow_active_condition_summary = cls(
            overdue_workflow_count=overdue_workflow_count,
            overdue_condition_count=overdue_condition_count,
            refresh_owners_truncated=refresh_owners_truncated,
            refresh_owners=refresh_owners,
            matching_workflow_count=matching_workflow_count,
            sampled_workflow_count=sampled_workflow_count,
            linked_workflow_count=linked_workflow_count,
            excluded_incomplete_workflow_count=excluded_incomplete_workflow_count,
            excluded_ambiguous_workflow_count=excluded_ambiguous_workflow_count,
            excluded_condition_evidence_workflow_count=excluded_condition_evidence_workflow_count,
            unclassified_workflow_count=unclassified_workflow_count,
            cohort_truncated=cohort_truncated,
            groups_truncated=groups_truncated,
            groups=groups,
        )

        return operation_workflow_active_condition_summary
