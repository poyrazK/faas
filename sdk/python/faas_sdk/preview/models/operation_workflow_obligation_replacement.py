from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_obligation_replacement_reason_kind import (
    OperationWorkflowObligationReplacementReasonKind,
    check_operation_workflow_obligation_replacement_reason_kind,
)

T = TypeVar("T", bound="OperationWorkflowObligationReplacement")


@_attrs_define
class OperationWorkflowObligationReplacement:
    """Same-source obligation succession recorded atomically with a source change. Links require overlapping saved case
    coverage and removed/new cycles or explicit transition references; a retained terminal same-kind predecessor may
    link a later cycle. Generic new-cycle reasons describe succession rather than business causation. Reinstated known
    identities, unrelated cases and missing history are not linked. These edges do not prove fulfillment.

    """

    predecessor_id: str
    successor_id: str
    predecessor_kind: str
    successor_kind: str
    reason: str
    reason_kind: OperationWorkflowObligationReplacementReasonKind

    def to_dict(self) -> dict[str, Any]:
        predecessor_id = self.predecessor_id

        successor_id = self.successor_id

        predecessor_kind = self.predecessor_kind

        successor_kind = self.successor_kind

        reason = self.reason

        reason_kind: str = self.reason_kind

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "predecessor_id": predecessor_id,
                "successor_id": successor_id,
                "predecessor_kind": predecessor_kind,
                "successor_kind": successor_kind,
                "reason": reason,
                "reason_kind": reason_kind,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        predecessor_id = d.pop("predecessor_id")

        successor_id = d.pop("successor_id")

        predecessor_kind = d.pop("predecessor_kind")

        successor_kind = d.pop("successor_kind")

        reason = d.pop("reason")

        reason_kind = check_operation_workflow_obligation_replacement_reason_kind(d.pop("reason_kind"))

        operation_workflow_obligation_replacement = cls(
            predecessor_id=predecessor_id,
            successor_id=successor_id,
            predecessor_kind=predecessor_kind,
            successor_kind=successor_kind,
            reason=reason,
            reason_kind=reason_kind,
        )

        return operation_workflow_obligation_replacement
