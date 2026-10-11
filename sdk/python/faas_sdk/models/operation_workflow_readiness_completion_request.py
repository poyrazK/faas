from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_completion_request_outcome import (
    OperationWorkflowReadinessCompletionRequestOutcome,
    check_operation_workflow_readiness_completion_request_outcome,
)
from ..types import UNSET, Unset

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
    expected_outcome_code: str | Unset = UNSET
    """OperationWorkflowReadinessCompletionRequest.expected_outcome_code. Optional caller-selected terminal outcome
    slug compared with application reports for the source workflow; never defines or modifies application success
    rules."""
    watch_verification: bool | Unset = UNSET
    """OperationWorkflowReadinessCompletionRequest.watch_verification. Opt in at completion to watch up to 16
    currently pending saved resolutions; requires comparable current pending evidence."""

    def to_dict(self) -> dict[str, Any]:
        outcome: str = self.outcome

        note = self.note

        evidence_operation_ids = []
        for evidence_operation_ids_item_data in self.evidence_operation_ids:
            evidence_operation_ids_item = str(evidence_operation_ids_item_data)
            evidence_operation_ids.append(evidence_operation_ids_item)

        expected_outcome_code = self.expected_outcome_code

        watch_verification = self.watch_verification

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "outcome": outcome,
                "note": note,
                "evidence_operation_ids": evidence_operation_ids,
            }
        )
        if expected_outcome_code is not UNSET:
            field_dict["expected_outcome_code"] = expected_outcome_code
        if watch_verification is not UNSET:
            field_dict["watch_verification"] = watch_verification

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

        expected_outcome_code = d.pop("expected_outcome_code", UNSET)

        watch_verification = d.pop("watch_verification", UNSET)

        operation_workflow_readiness_completion_request = cls(
            outcome=outcome,
            note=note,
            evidence_operation_ids=evidence_operation_ids,
            expected_outcome_code=expected_outcome_code,
            watch_verification=watch_verification,
        )

        return operation_workflow_readiness_completion_request
