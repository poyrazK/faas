from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.app_webhook_delivery_response import AppWebhookDeliveryResponse


T = TypeVar("T", bound="AppWebhookRetryDeliveryResponse")


@_attrs_define
class AppWebhookRetryDeliveryResponse:
    """Response from POST /v1/apps/{slug}/webhooks/{id}/deliveries/{did}/retry.
    Mirrors AppWebhookDeliveryResponse; the row in `delivery` is
    re-emitted with status='pending' and next_attempt_at=now().

    """

    delivery: AppWebhookDeliveryResponse
    """One row per (event × target) emission. The dispatcher mutates
    this row in place as attempts progress; the GET /deliveries
    endpoint returns this shape per row. attempt=7 + status='dead'
    is the DLQ state; the customer-facing retry endpoint flips a
    dead row back to status='pending' for one more shot.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        delivery = self.delivery.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "delivery": delivery,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_webhook_delivery_response import AppWebhookDeliveryResponse

        d = dict(src_dict)
        delivery = AppWebhookDeliveryResponse.from_dict(d.pop("delivery"))

        app_webhook_retry_delivery_response = cls(
            delivery=delivery,
        )

        app_webhook_retry_delivery_response.additional_properties = d
        return app_webhook_retry_delivery_response

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
