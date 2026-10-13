from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_obligation_event_kind import (
    OperationWorkflowObligationEventKind,
    check_operation_workflow_obligation_event_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowObligationEvent")


@_attrs_define
class OperationWorkflowObligationEvent:
    """Retained source receipt anchored to the saved obligation; observed_assignment describes the delivered assignment
    snapshot, not a newly audited write. Available event coverage is partial.

    """

    kind: OperationWorkflowObligationEventKind
    at: datetime.datetime
    explanation: str
    obligation_id: str | Unset = UNSET
    """Exact obligation identity for mutation-time receipts; older inferred observations may omit it."""
    actor: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        at = self.at.isoformat()

        explanation = self.explanation

        obligation_id = self.obligation_id

        actor = self.actor

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "at": at,
                "explanation": explanation,
            }
        )
        if obligation_id is not UNSET:
            field_dict["obligation_id"] = obligation_id
        if actor is not UNSET:
            field_dict["actor"] = actor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_operation_workflow_obligation_event_kind(d.pop("kind"))

        at = datetime.datetime.fromisoformat(d.pop("at"))

        explanation = d.pop("explanation")

        obligation_id = d.pop("obligation_id", UNSET)

        actor = d.pop("actor", UNSET)

        operation_workflow_obligation_event = cls(
            kind=kind,
            at=at,
            explanation=explanation,
            obligation_id=obligation_id,
            actor=actor,
        )

        return operation_workflow_obligation_event
