from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRecoveryCancellationRequest")


@_attrs_define
class WorkflowRecoveryCancellationRequest:
    """Exact admitted recovery item and generation reviewed by an operator. Preview is advisory and commit rechecks
    eligibility. Operator identity is resolved from authentication.

    """

    position: int
    expected_resume_count: int
    reason: str
    """Nonblank UTF-8 reason without control characters. Limited to 512 bytes."""
    request_id: UUID | Unset = UNSET
    """Optional stable action identifier. Apply retains the original outcome including refusals; reusing it with
    changed arguments conflicts. Preview never writes a receipt."""

    def to_dict(self) -> dict[str, Any]:
        position = self.position

        expected_resume_count = self.expected_resume_count

        reason = self.reason

        request_id: str | Unset = UNSET
        if not isinstance(self.request_id, Unset):
            request_id = str(self.request_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "position": position,
                "expected_resume_count": expected_resume_count,
                "reason": reason,
            }
        )
        if request_id is not UNSET:
            field_dict["request_id"] = request_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        position = d.pop("position")

        expected_resume_count = d.pop("expected_resume_count")

        reason = d.pop("reason")

        _request_id = d.pop("request_id", UNSET)
        request_id: UUID | Unset
        if isinstance(_request_id, Unset):
            request_id = UNSET
        else:
            request_id = UUID(_request_id)

        workflow_recovery_cancellation_request = cls(
            position=position,
            expected_resume_count=expected_resume_count,
            reason=reason,
            request_id=request_id,
        )

        return workflow_recovery_cancellation_request
