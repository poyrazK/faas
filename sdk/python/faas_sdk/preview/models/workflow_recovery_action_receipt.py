from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_recovery_cancellation_request import WorkflowRecoveryCancellationRequest
    from ..models.workflow_recovery_cancellation_response import WorkflowRecoveryCancellationResponse


T = TypeVar("T", bound="WorkflowRecoveryActionReceipt")


@_attrs_define
class WorkflowRecoveryActionReceipt:
    """Durable cancellation decision retained for the lifetime of its recovery job. The same action identifier returns the
    original outcome even after the run advances. Operator identity is resolved from authentication.

    """

    request: WorkflowRecoveryCancellationRequest
    """Exact admitted recovery item and generation reviewed by an operator. Preview is advisory and commit rechecks
    eligibility. Operator identity is resolved from authentication."""
    response: WorkflowRecoveryCancellationResponse
    """Read-only preview or cancellation result. Only eligible generations can be cancelled. Repeating a
    cancellation against the same cancelled generation does not append another history entry."""
    captured_at: datetime.datetime
    actor_kind: str
    actor_id: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request = self.request.to_dict()

        response = self.response.to_dict()

        captured_at = self.captured_at.isoformat()

        actor_kind = self.actor_kind

        actor_id = self.actor_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request": request,
                "response": response,
                "captured_at": captured_at,
                "actor_kind": actor_kind,
                "actor_id": actor_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_cancellation_request import WorkflowRecoveryCancellationRequest
        from ..models.workflow_recovery_cancellation_response import WorkflowRecoveryCancellationResponse

        d = dict(src_dict)
        request = WorkflowRecoveryCancellationRequest.from_dict(d.pop("request"))

        response = WorkflowRecoveryCancellationResponse.from_dict(d.pop("response"))

        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        actor_kind = d.pop("actor_kind")

        actor_id = d.pop("actor_id")

        workflow_recovery_action_receipt = cls(
            request=request,
            response=response,
            captured_at=captured_at,
            actor_kind=actor_kind,
            actor_id=actor_id,
        )

        workflow_recovery_action_receipt.additional_properties = d
        return workflow_recovery_action_receipt

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
