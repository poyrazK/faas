from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ReplayDevBridgeWebhookRequest")


@_attrs_define
class ReplayDevBridgeWebhookRequest:
    """Explicit verified receipt selection with scoped routing authority."""

    invocation_id: UUID
    request_token: str
    idempotency_key: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        invocation_id = str(self.invocation_id)

        request_token = self.request_token

        idempotency_key = self.idempotency_key

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "invocation_id": invocation_id,
                "request_token": request_token,
                "idempotency_key": idempotency_key,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        invocation_id = UUID(d.pop("invocation_id"))

        request_token = d.pop("request_token")

        idempotency_key = d.pop("idempotency_key")

        replay_dev_bridge_webhook_request = cls(
            invocation_id=invocation_id,
            request_token=request_token,
            idempotency_key=idempotency_key,
        )

        replay_dev_bridge_webhook_request.additional_properties = d
        return replay_dev_bridge_webhook_request

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
