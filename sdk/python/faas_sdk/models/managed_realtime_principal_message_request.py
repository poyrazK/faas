from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.managed_realtime_principal_message_request_delivery import (
    ManagedRealtimePrincipalMessageRequestDelivery,
    check_managed_realtime_principal_message_request_delivery,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimePrincipalMessageRequest")


@_attrs_define
class ManagedRealtimePrincipalMessageRequest:
    """Principal message payload with optional retained notification and push fallback settings."""

    principal: str
    data_base64: str
    """Standard base64; at most 4096 decoded bytes."""
    binary: bool | Unset = False
    delivery: ManagedRealtimePrincipalMessageRequestDelivery | Unset = "live"
    message_id: str | Unset = UNSET
    request_receipt: bool | Unset = False

    def to_dict(self) -> dict[str, Any]:
        principal = self.principal

        data_base64 = self.data_base64

        binary = self.binary

        delivery: str | Unset = UNSET
        if not isinstance(self.delivery, Unset):
            delivery = self.delivery

        message_id = self.message_id

        request_receipt = self.request_receipt

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "principal": principal,
                "data_base64": data_base64,
            }
        )
        if binary is not UNSET:
            field_dict["binary"] = binary
        if delivery is not UNSET:
            field_dict["delivery"] = delivery
        if message_id is not UNSET:
            field_dict["message_id"] = message_id
        if request_receipt is not UNSET:
            field_dict["request_receipt"] = request_receipt

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        principal = d.pop("principal")

        data_base64 = d.pop("data_base64")

        binary = d.pop("binary", UNSET)

        _delivery = d.pop("delivery", UNSET)
        delivery: ManagedRealtimePrincipalMessageRequestDelivery | Unset
        if isinstance(_delivery, Unset):
            delivery = UNSET
        else:
            delivery = check_managed_realtime_principal_message_request_delivery(_delivery)

        message_id = d.pop("message_id", UNSET)

        request_receipt = d.pop("request_receipt", UNSET)

        managed_realtime_principal_message_request = cls(
            principal=principal,
            data_base64=data_base64,
            binary=binary,
            delivery=delivery,
            message_id=message_id,
            request_receipt=request_receipt,
        )

        return managed_realtime_principal_message_request
