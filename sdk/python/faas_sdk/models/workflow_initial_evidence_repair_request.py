from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="WorkflowInitialEvidenceRepairRequest")


@_attrs_define
class WorkflowInitialEvidenceRepairRequest:
    """Policy-fenced request to repair retained initial recovery evidence."""

    request_id: UUID
    reason: str

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        reason = d.pop("reason")

        workflow_initial_evidence_repair_request = cls(
            request_id=request_id,
            reason=reason,
        )

        return workflow_initial_evidence_repair_request
