from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_review_response_link import OperationWorkflowReviewResponseLink


T = TypeVar("T", bound="OperationWorkflowReviewResponseEscalationCompletion")


@_attrs_define
class OperationWorkflowReviewResponseEscalationCompletion:
    """Immutable human completion of escalation coordination work, with linked review references and server actor and time.
    Existing overdue markers remain historical.

    """

    acknowledged_at: datetime.datetime
    summary: str
    evidence: list[OperationWorkflowReviewResponseLink]
    at: datetime.datetime
    actor: str

    def to_dict(self) -> dict[str, Any]:
        acknowledged_at = self.acknowledged_at.isoformat()

        summary = self.summary

        evidence = []
        for evidence_item_data in self.evidence:
            evidence_item = evidence_item_data.to_dict()
            evidence.append(evidence_item)

        at = self.at.isoformat()

        actor = self.actor

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "acknowledged_at": acknowledged_at,
                "summary": summary,
                "evidence": evidence,
                "at": at,
                "actor": actor,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_review_response_link import OperationWorkflowReviewResponseLink

        d = dict(src_dict)
        acknowledged_at = datetime.datetime.fromisoformat(d.pop("acknowledged_at"))

        summary = d.pop("summary")

        evidence = []
        _evidence = d.pop("evidence")
        for evidence_item_data in _evidence:
            evidence_item = OperationWorkflowReviewResponseLink.from_dict(evidence_item_data)

            evidence.append(evidence_item)

        at = datetime.datetime.fromisoformat(d.pop("at"))

        actor = d.pop("actor")

        operation_workflow_review_response_escalation_completion = cls(
            acknowledged_at=acknowledged_at,
            summary=summary,
            evidence=evidence,
            at=at,
            actor=actor,
        )

        return operation_workflow_review_response_escalation_completion
