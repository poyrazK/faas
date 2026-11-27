from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.api_sandbox_stripe_webhook_data_object import APISandboxStripeWebhookDataObject


T = TypeVar("T", bound="APISandboxStripeWebhook")


@_attrs_define
class APISandboxStripeWebhook:
    """Owner-only synthetic Stripe event fixture for an automated run."""

    delivery_path: str
    """Sandbox app route without a query or fragment."""
    event_type: str
    data_object: APISandboxStripeWebhookDataObject
    """Fixture for Stripe data.object, up to 8 KiB as JSON."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        delivery_path = self.delivery_path

        event_type = self.event_type

        data_object = self.data_object.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "delivery_path": delivery_path,
                "event_type": event_type,
                "data_object": data_object,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_stripe_webhook_data_object import APISandboxStripeWebhookDataObject

        d = dict(src_dict)
        delivery_path = d.pop("delivery_path")

        event_type = d.pop("event_type")

        data_object = APISandboxStripeWebhookDataObject.from_dict(d.pop("data_object"))

        api_sandbox_stripe_webhook = cls(
            delivery_path=delivery_path,
            event_type=event_type,
            data_object=data_object,
        )

        api_sandbox_stripe_webhook.additional_properties = d
        return api_sandbox_stripe_webhook

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
