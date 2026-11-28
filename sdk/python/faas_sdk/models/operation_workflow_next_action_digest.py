from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_next_action_digest_explanation import OperationWorkflowNextActionDigestExplanation
    from ..models.operation_workflow_next_action_queue_item import OperationWorkflowNextActionQueueItem


T = TypeVar("T", bound="OperationWorkflowNextActionDigest")


@_attrs_define
class OperationWorkflowNextActionDigest:
    """One bounded changed-item team digest. Items are advisory snapshots, not application authorization; source state may
    change before reading or publication. Fingerprints exclude polling timestamps and are recorded atomically with
    delivery under the subscription lease. Acknowledgment changes no source obligation.

    """

    items: list[OperationWorkflowNextActionQueueItem]
    owner: str
    scope: str
    evaluated_at: datetime.datetime
    soon_until: datetime.datetime
    matching_count: int
    """All retained qualifying action rows, including unchanged items; not a workflow or global task count."""
    changed_count: int
    """Newly qualifying or changed action rows pending delivery at this evaluation."""
    remaining_changed_count: int
    """Changed rows beyond this delivery cap, which remain eligible at later cadence evaluations."""
    retention_limited: bool
    """Queue and source inbox retention bound coverage. Deleted/pruned source work is unavailable."""
    explanations: list[OperationWorkflowNextActionDigestExplanation] | Unset = UNSET
    """Delivery explanations identified by source subscription, sequence and action kind. Older notifications may
    omit these."""

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        owner = self.owner

        scope = self.scope

        evaluated_at = self.evaluated_at.isoformat()

        soon_until = self.soon_until.isoformat()

        matching_count = self.matching_count

        changed_count = self.changed_count

        remaining_changed_count = self.remaining_changed_count

        retention_limited = self.retention_limited

        explanations: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.explanations, Unset):
            explanations = []
            for explanations_item_data in self.explanations:
                explanations_item = explanations_item_data.to_dict()
                explanations.append(explanations_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "items": items,
                "owner": owner,
                "scope": scope,
                "evaluated_at": evaluated_at,
                "soon_until": soon_until,
                "matching_count": matching_count,
                "changed_count": changed_count,
                "remaining_changed_count": remaining_changed_count,
                "retention_limited": retention_limited,
            }
        )
        if explanations is not UNSET:
            field_dict["explanations"] = explanations

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_next_action_digest_explanation import (
            OperationWorkflowNextActionDigestExplanation,
        )
        from ..models.operation_workflow_next_action_queue_item import OperationWorkflowNextActionQueueItem

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowNextActionQueueItem.from_dict(items_item_data)

            items.append(items_item)

        owner = d.pop("owner")

        scope = d.pop("scope")

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        soon_until = datetime.datetime.fromisoformat(d.pop("soon_until"))

        matching_count = d.pop("matching_count")

        changed_count = d.pop("changed_count")

        remaining_changed_count = d.pop("remaining_changed_count")

        retention_limited = d.pop("retention_limited")

        _explanations = d.pop("explanations", UNSET)
        explanations: list[OperationWorkflowNextActionDigestExplanation] | Unset = UNSET
        if _explanations is not UNSET:
            explanations = []
            for explanations_item_data in _explanations:
                explanations_item = OperationWorkflowNextActionDigestExplanation.from_dict(explanations_item_data)

                explanations.append(explanations_item)

        operation_workflow_next_action_digest = cls(
            items=items,
            owner=owner,
            scope=scope,
            evaluated_at=evaluated_at,
            soon_until=soon_until,
            matching_count=matching_count,
            changed_count=changed_count,
            remaining_changed_count=remaining_changed_count,
            retention_limited=retention_limited,
            explanations=explanations,
        )

        return operation_workflow_next_action_digest
