from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_next_action_digest_explanation_changed_fields_item import (
    OperationWorkflowNextActionDigestExplanationChangedFieldsItem,
    check_operation_workflow_next_action_digest_explanation_changed_fields_item,
)
from ..models.operation_workflow_next_action_digest_explanation_comparison import (
    OperationWorkflowNextActionDigestExplanationComparison,
    check_operation_workflow_next_action_digest_explanation_comparison,
)
from ..models.operation_workflow_next_action_digest_explanation_reasons_item import (
    OperationWorkflowNextActionDigestExplanationReasonsItem,
    check_operation_workflow_next_action_digest_explanation_reasons_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowNextActionDigestExplanation")


@_attrs_define
class OperationWorkflowNextActionDigestExplanation:
    """Why one delivered action qualified, which projected fields changed since its last delivered snapshot, and advisory
    next steps. Missing historical snapshots never imply no changes; absence from a digest does not prove completion.
    Evidence comparisons describe references, not live evidence health or escalation state.

    """

    subscription_id: UUID
    sequence: int
    action_kind: str
    reasons: list[OperationWorkflowNextActionDigestExplanationReasonsItem]
    comparison: OperationWorkflowNextActionDigestExplanationComparison
    changed_fields: list[OperationWorkflowNextActionDigestExplanationChangedFieldsItem]
    next_step: str
    obligation_id: str | Unset = UNSET
    """Exact work-item identity, distinguishing multiple fulfillment reviews on one shared source."""

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        sequence = self.sequence

        action_kind = self.action_kind

        reasons = []
        for reasons_item_data in self.reasons:
            reasons_item: str = reasons_item_data
            reasons.append(reasons_item)

        comparison: str = self.comparison

        changed_fields = []
        for changed_fields_item_data in self.changed_fields:
            changed_fields_item: str = changed_fields_item_data
            changed_fields.append(changed_fields_item)

        next_step = self.next_step

        obligation_id = self.obligation_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "sequence": sequence,
                "action_kind": action_kind,
                "reasons": reasons,
                "comparison": comparison,
                "changed_fields": changed_fields,
                "next_step": next_step,
            }
        )
        if obligation_id is not UNSET:
            field_dict["obligation_id"] = obligation_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        sequence = d.pop("sequence")

        action_kind = d.pop("action_kind")

        reasons = []
        _reasons = d.pop("reasons")
        for reasons_item_data in _reasons:
            reasons_item = check_operation_workflow_next_action_digest_explanation_reasons_item(reasons_item_data)

            reasons.append(reasons_item)

        comparison = check_operation_workflow_next_action_digest_explanation_comparison(d.pop("comparison"))

        changed_fields = []
        _changed_fields = d.pop("changed_fields")
        for changed_fields_item_data in _changed_fields:
            changed_fields_item = check_operation_workflow_next_action_digest_explanation_changed_fields_item(
                changed_fields_item_data
            )

            changed_fields.append(changed_fields_item)

        next_step = d.pop("next_step")

        obligation_id = d.pop("obligation_id", UNSET)

        operation_workflow_next_action_digest_explanation = cls(
            subscription_id=subscription_id,
            sequence=sequence,
            action_kind=action_kind,
            reasons=reasons,
            comparison=comparison,
            changed_fields=changed_fields,
            next_step=next_step,
            obligation_id=obligation_id,
        )

        return operation_workflow_next_action_digest_explanation
