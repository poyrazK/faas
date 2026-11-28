from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_consumer_billing_usage_progress_status import (
    APIConsumerBillingUsageProgressStatus,
    check_api_consumer_billing_usage_progress_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="APIConsumerBillingUsageProgress")


@_attrs_define
class APIConsumerBillingUsageProgress:
    """Summary of recent durable requests and priced usage through the last complete minute."""

    since: datetime.datetime
    until: datetime.datetime
    status: APIConsumerBillingUsageProgressStatus
    request_count: int
    unpriced_units: int
    amount_millicents: int
    currency: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        since = self.since.isoformat()

        until = self.until.isoformat()

        status: str = self.status

        request_count = self.request_count

        unpriced_units = self.unpriced_units

        amount_millicents = self.amount_millicents

        currency = self.currency

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "since": since,
                "until": until,
                "status": status,
                "request_count": request_count,
                "unpriced_units": unpriced_units,
                "amount_millicents": amount_millicents,
            }
        )
        if currency is not UNSET:
            field_dict["currency"] = currency

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        since = datetime.datetime.fromisoformat(d.pop("since"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        status = check_api_consumer_billing_usage_progress_status(d.pop("status"))

        request_count = d.pop("request_count")

        unpriced_units = d.pop("unpriced_units")

        amount_millicents = d.pop("amount_millicents")

        currency = d.pop("currency", UNSET)

        api_consumer_billing_usage_progress = cls(
            since=since,
            until=until,
            status=status,
            request_count=request_count,
            unpriced_units=unpriced_units,
            amount_millicents=amount_millicents,
            currency=currency,
        )

        api_consumer_billing_usage_progress.additional_properties = d
        return api_consumer_billing_usage_progress

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
