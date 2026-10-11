from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operation_submission_lookup_response_state import (
    OperationSubmissionLookupResponseState,
    check_operation_submission_lookup_response_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_accepted_response import OperationAcceptedResponse


T = TypeVar("T", bound="OperationSubmissionLookupResponse")


@_attrs_define
class OperationSubmissionLookupResponse:
    """Read-only observation; accepted includes receipt and accepted_at. unresolved never proves rejection."""

    state: OperationSubmissionLookupResponseState
    receipt: OperationAcceptedResponse | Unset = UNSET
    """Durable stable operation identity and customer-scoped read routes."""
    accepted_at: datetime.datetime | Unset = UNSET
    idempotency_expires_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        receipt: dict[str, Any] | Unset = UNSET
        if not isinstance(self.receipt, Unset):
            receipt = self.receipt.to_dict()

        accepted_at: str | Unset = UNSET
        if not isinstance(self.accepted_at, Unset):
            accepted_at = self.accepted_at.isoformat()

        idempotency_expires_at: str | Unset = UNSET
        if not isinstance(self.idempotency_expires_at, Unset):
            idempotency_expires_at = self.idempotency_expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
            }
        )
        if receipt is not UNSET:
            field_dict["receipt"] = receipt
        if accepted_at is not UNSET:
            field_dict["accepted_at"] = accepted_at
        if idempotency_expires_at is not UNSET:
            field_dict["idempotency_expires_at"] = idempotency_expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_accepted_response import OperationAcceptedResponse

        d = dict(src_dict)
        state = check_operation_submission_lookup_response_state(d.pop("state"))

        _receipt = d.pop("receipt", UNSET)
        receipt: OperationAcceptedResponse | Unset
        if isinstance(_receipt, Unset):
            receipt = UNSET
        else:
            receipt = OperationAcceptedResponse.from_dict(_receipt)

        _accepted_at = d.pop("accepted_at", UNSET)
        accepted_at: datetime.datetime | Unset
        if isinstance(_accepted_at, Unset):
            accepted_at = UNSET
        else:
            accepted_at = datetime.datetime.fromisoformat(_accepted_at)

        _idempotency_expires_at = d.pop("idempotency_expires_at", UNSET)
        idempotency_expires_at: datetime.datetime | Unset
        if isinstance(_idempotency_expires_at, Unset):
            idempotency_expires_at = UNSET
        else:
            idempotency_expires_at = datetime.datetime.fromisoformat(_idempotency_expires_at)

        operation_submission_lookup_response = cls(
            state=state,
            receipt=receipt,
            accepted_at=accepted_at,
            idempotency_expires_at=idempotency_expires_at,
        )

        operation_submission_lookup_response.additional_properties = d
        return operation_submission_lookup_response

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
