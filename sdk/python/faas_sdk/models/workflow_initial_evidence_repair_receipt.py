from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_initial_evidence_repair_receipt_outcome import (
    WorkflowInitialEvidenceRepairReceiptOutcome,
    check_workflow_initial_evidence_repair_receipt_outcome,
)

T = TypeVar("T", bound="WorkflowInitialEvidenceRepairReceipt")


@_attrs_define
class WorkflowInitialEvidenceRepairReceipt:
    """Recorded result of an initial recovery evidence repair."""

    request_id: UUID
    reason: str
    actor_kind: str
    actor_id: str
    repaired_at: datetime.datetime
    outcome: WorkflowInitialEvidenceRepairReceiptOutcome
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        reason = self.reason

        actor_kind = self.actor_kind

        actor_id = self.actor_id

        repaired_at = self.repaired_at.isoformat()

        outcome: str = self.outcome

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "reason": reason,
                "actor_kind": actor_kind,
                "actor_id": actor_id,
                "repaired_at": repaired_at,
                "outcome": outcome,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        reason = d.pop("reason")

        actor_kind = d.pop("actor_kind")

        actor_id = d.pop("actor_id")

        repaired_at = datetime.datetime.fromisoformat(d.pop("repaired_at"))

        outcome = check_workflow_initial_evidence_repair_receipt_outcome(d.pop("outcome"))

        workflow_initial_evidence_repair_receipt = cls(
            request_id=request_id,
            reason=reason,
            actor_kind=actor_kind,
            actor_id=actor_id,
            repaired_at=repaired_at,
            outcome=outcome,
        )

        workflow_initial_evidence_repair_receipt.additional_properties = d
        return workflow_initial_evidence_repair_receipt

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
