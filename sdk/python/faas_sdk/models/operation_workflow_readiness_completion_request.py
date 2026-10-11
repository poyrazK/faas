from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_completion_request_outcome import (
    OperationWorkflowReadinessCompletionRequestOutcome,
    check_operation_workflow_readiness_completion_request_outcome,
)

T = TypeVar("T", bound="OperationWorkflowReadinessCompletionRequest")


@_attrs_define
class OperationWorkflowReadinessCompletionRequest:
    """An asserted coordination outcome with a required note; resolved requires at least one retained evidence Operation.
    Closing does not prove business resolution.

    """

    outcome: OperationWorkflowReadinessCompletionRequestOutcome
    """OperationWorkflowReadinessCompletionRequest.outcome. Retained follow-up completion value; never changes
    application evidence."""
    note: str
    """OperationWorkflowReadinessCompletionRequest.note. Retained follow-up completion value; never changes
    application evidence."""
    evidence_operation_ids: list[UUID]
    """OperationWorkflowReadinessCompletionRequest.evidence_operation_ids. Retained follow-up completion value;
    never changes application evidence."""

    def to_dict(self) -> dict[str, Any]:
        outcome: str = self.outcome

        note = self.note

        evidence_operation_ids = []
        for evidence_operation_ids_item_data in self.evidence_operation_ids:
            evidence_operation_ids_item = str(evidence_operation_ids_item_data)
            evidence_operation_ids.append(evidence_operation_ids_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "outcome": outcome,
                "note": note,
                "evidence_operation_ids": evidence_operation_ids,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        outcome = check_operation_workflow_readiness_completion_request_outcome(d.pop("outcome"))

        note = d.pop("note")

        evidence_operation_ids = []
        _evidence_operation_ids = d.pop("evidence_operation_ids")
        for evidence_operation_ids_item_data in _evidence_operation_ids:
            evidence_operation_ids_item = UUID(evidence_operation_ids_item_data)

            evidence_operation_ids.append(evidence_operation_ids_item)

        operation_workflow_readiness_completion_request = cls(
            outcome=outcome,
            note=note,
            evidence_operation_ids=evidence_operation_ids,
        )

        return operation_workflow_readiness_completion_request
