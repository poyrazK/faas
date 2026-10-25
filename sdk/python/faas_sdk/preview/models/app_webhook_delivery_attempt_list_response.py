from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_webhook_delivery_attempt_response import AppWebhookDeliveryAttemptResponse


T = TypeVar("T", bound="AppWebhookDeliveryAttemptListResponse")


@_attrs_define
class AppWebhookDeliveryAttemptListResponse:
    """A newest-first page of attempts for one account-owned delivery."""

    attempts: list[AppWebhookDeliveryAttemptResponse]
    next_token: str | Unset = UNSET
    """Cursor for the next page."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        attempts = []
        for attempts_item_data in self.attempts:
            attempts_item = attempts_item_data.to_dict()
            attempts.append(attempts_item)

        next_token = self.next_token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attempts": attempts,
            }
        )
        if next_token is not UNSET:
            field_dict["next_token"] = next_token

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_webhook_delivery_attempt_response import AppWebhookDeliveryAttemptResponse

        d = dict(src_dict)
        attempts = []
        _attempts = d.pop("attempts")
        for attempts_item_data in _attempts:
            attempts_item = AppWebhookDeliveryAttemptResponse.from_dict(attempts_item_data)

            attempts.append(attempts_item)

        next_token = d.pop("next_token", UNSET)

        app_webhook_delivery_attempt_list_response = cls(
            attempts=attempts,
            next_token=next_token,
        )

        app_webhook_delivery_attempt_list_response.additional_properties = d
        return app_webhook_delivery_attempt_list_response

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
