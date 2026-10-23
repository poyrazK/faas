from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="APIConsumerClosedWebhookPayload")


@_attrs_define
class APIConsumerClosedWebhookPayload:
    """Data delivered with consumer.closed when a customer closed their account with their own key."""

    app_id: UUID
    consumer_id: UUID
    external_ref: str
    closed_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        consumer_id = str(self.consumer_id)

        external_ref = self.external_ref

        closed_at = self.closed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "consumer_id": consumer_id,
                "external_ref": external_ref,
                "closed_at": closed_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        consumer_id = UUID(d.pop("consumer_id"))

        external_ref = d.pop("external_ref")

        closed_at = datetime.datetime.fromisoformat(d.pop("closed_at"))

        api_consumer_closed_webhook_payload = cls(
            app_id=app_id,
            consumer_id=consumer_id,
            external_ref=external_ref,
            closed_at=closed_at,
        )

        api_consumer_closed_webhook_payload.additional_properties = d
        return api_consumer_closed_webhook_payload

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
