from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.billing_run_item_response import BillingRunItemResponse
    from ..models.billing_run_usage_hold_hour import BillingRunUsageHoldHour


T = TypeVar("T", bound="BillingRunUsageHold")


@_attrs_define
class BillingRunUsageHold:
    """Billing run usage hold evidence and its current ledger comparison."""

    item: BillingRunItemResponse
    """What a billing run did for one app consumer, platform tenant or Product Control customer. A product_control
    item names its app in app_id and its customer in platform_tenant_id; its statement_id is the Product Control
    statement document."""
    missing_requests: int
    """Minimum successful requests still missing from the ledger across recorded hours."""
    hours: list[BillingRunUsageHoldHour]
    held_at: datetime.datetime | Unset = UNSET
    """When the run recorded the original gap."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        item = self.item.to_dict()

        missing_requests = self.missing_requests

        hours = []
        for hours_item_data in self.hours:
            hours_item = hours_item_data.to_dict()
            hours.append(hours_item)

        held_at: str | Unset = UNSET
        if not isinstance(self.held_at, Unset):
            held_at = self.held_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "item": item,
                "missing_requests": missing_requests,
                "hours": hours,
            }
        )
        if held_at is not UNSET:
            field_dict["held_at"] = held_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.billing_run_item_response import BillingRunItemResponse
        from ..models.billing_run_usage_hold_hour import BillingRunUsageHoldHour

        d = dict(src_dict)
        item = BillingRunItemResponse.from_dict(d.pop("item"))

        missing_requests = d.pop("missing_requests")

        hours = []
        _hours = d.pop("hours")
        for hours_item_data in _hours:
            hours_item = BillingRunUsageHoldHour.from_dict(hours_item_data)

            hours.append(hours_item)

        _held_at = d.pop("held_at", UNSET)
        held_at: datetime.datetime | Unset
        if isinstance(_held_at, Unset):
            held_at = UNSET
        else:
            held_at = datetime.datetime.fromisoformat(_held_at)

        billing_run_usage_hold = cls(
            item=item,
            missing_requests=missing_requests,
            hours=hours,
            held_at=held_at,
        )

        billing_run_usage_hold.additional_properties = d
        return billing_run_usage_hold

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
