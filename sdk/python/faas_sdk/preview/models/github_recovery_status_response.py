from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.github_check_update_record import GithubCheckUpdateRecord
    from ..models.github_webhook_delivery_record import GithubWebhookDeliveryRecord


T = TypeVar("T", bound="GithubRecoveryStatusResponse")


@_attrs_define
class GithubRecoveryStatusResponse:
    """Operator-safe projections of githubd's durable recovery queues. Webhook payloads are never included."""

    deliveries: list[GithubWebhookDeliveryRecord]
    check_updates: list[GithubCheckUpdateRecord]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deliveries = []
        for deliveries_item_data in self.deliveries:
            deliveries_item = deliveries_item_data.to_dict()
            deliveries.append(deliveries_item)

        check_updates = []
        for check_updates_item_data in self.check_updates:
            check_updates_item = check_updates_item_data.to_dict()
            check_updates.append(check_updates_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deliveries": deliveries,
                "check_updates": check_updates,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.github_check_update_record import GithubCheckUpdateRecord
        from ..models.github_webhook_delivery_record import GithubWebhookDeliveryRecord

        d = dict(src_dict)
        deliveries = []
        _deliveries = d.pop("deliveries")
        for deliveries_item_data in _deliveries:
            deliveries_item = GithubWebhookDeliveryRecord.from_dict(deliveries_item_data)

            deliveries.append(deliveries_item)

        check_updates = []
        _check_updates = d.pop("check_updates")
        for check_updates_item_data in _check_updates:
            check_updates_item = GithubCheckUpdateRecord.from_dict(check_updates_item_data)

            check_updates.append(check_updates_item)

        github_recovery_status_response = cls(
            deliveries=deliveries,
            check_updates=check_updates,
        )

        github_recovery_status_response.additional_properties = d
        return github_recovery_status_response

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
