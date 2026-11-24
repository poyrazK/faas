from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_consumer_quote_breakdown import APIConsumerQuoteBreakdown
    from ..models.api_consumer_usage_quote_bucket_response import APIConsumerUsageQuoteBucketResponse


T = TypeVar("T", bound="APIConsumerUsageQuoteResponse")


@_attrs_define
class APIConsumerUsageQuoteResponse:
    """Deterministic estimate from durable API consumer usage and versioned app pricing; not an invoice."""

    consumer_id: UUID
    period_start: datetime.datetime
    period_end: datetime.datetime
    billable_units: int
    unpriced_units: int
    amount_millicents: int
    priced: bool
    buckets: list[APIConsumerUsageQuoteBucketResponse]
    as_of: datetime.datetime
    route_breakdown: list[APIConsumerQuoteBreakdown] | Unset = UNSET
    meter_breakdown: list[APIConsumerQuoteBreakdown] | Unset = UNSET
    currency: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        consumer_id = str(self.consumer_id)

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        billable_units = self.billable_units

        unpriced_units = self.unpriced_units

        amount_millicents = self.amount_millicents

        priced = self.priced

        buckets = []
        for buckets_item_data in self.buckets:
            buckets_item = buckets_item_data.to_dict()
            buckets.append(buckets_item)

        as_of = self.as_of.isoformat()

        route_breakdown: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.route_breakdown, Unset):
            route_breakdown = []
            for route_breakdown_item_data in self.route_breakdown:
                route_breakdown_item = route_breakdown_item_data.to_dict()
                route_breakdown.append(route_breakdown_item)

        meter_breakdown: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.meter_breakdown, Unset):
            meter_breakdown = []
            for meter_breakdown_item_data in self.meter_breakdown:
                meter_breakdown_item = meter_breakdown_item_data.to_dict()
                meter_breakdown.append(meter_breakdown_item)

        currency = self.currency

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "consumer_id": consumer_id,
                "period_start": period_start,
                "period_end": period_end,
                "billable_units": billable_units,
                "unpriced_units": unpriced_units,
                "amount_millicents": amount_millicents,
                "priced": priced,
                "buckets": buckets,
                "as_of": as_of,
            }
        )
        if route_breakdown is not UNSET:
            field_dict["route_breakdown"] = route_breakdown
        if meter_breakdown is not UNSET:
            field_dict["meter_breakdown"] = meter_breakdown
        if currency is not UNSET:
            field_dict["currency"] = currency

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_quote_breakdown import APIConsumerQuoteBreakdown
        from ..models.api_consumer_usage_quote_bucket_response import APIConsumerUsageQuoteBucketResponse

        d = dict(src_dict)
        consumer_id = UUID(d.pop("consumer_id"))

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        billable_units = d.pop("billable_units")

        unpriced_units = d.pop("unpriced_units")

        amount_millicents = d.pop("amount_millicents")

        priced = d.pop("priced")

        buckets = []
        _buckets = d.pop("buckets")
        for buckets_item_data in _buckets:
            buckets_item = APIConsumerUsageQuoteBucketResponse.from_dict(buckets_item_data)

            buckets.append(buckets_item)

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        _route_breakdown = d.pop("route_breakdown", UNSET)
        route_breakdown: list[APIConsumerQuoteBreakdown] | Unset = UNSET
        if _route_breakdown is not UNSET:
            route_breakdown = []
            for route_breakdown_item_data in _route_breakdown:
                route_breakdown_item = APIConsumerQuoteBreakdown.from_dict(route_breakdown_item_data)

                route_breakdown.append(route_breakdown_item)

        _meter_breakdown = d.pop("meter_breakdown", UNSET)
        meter_breakdown: list[APIConsumerQuoteBreakdown] | Unset = UNSET
        if _meter_breakdown is not UNSET:
            meter_breakdown = []
            for meter_breakdown_item_data in _meter_breakdown:
                meter_breakdown_item = APIConsumerQuoteBreakdown.from_dict(meter_breakdown_item_data)

                meter_breakdown.append(meter_breakdown_item)

        currency = d.pop("currency", UNSET)

        api_consumer_usage_quote_response = cls(
            consumer_id=consumer_id,
            period_start=period_start,
            period_end=period_end,
            billable_units=billable_units,
            unpriced_units=unpriced_units,
            amount_millicents=amount_millicents,
            priced=priced,
            buckets=buckets,
            as_of=as_of,
            route_breakdown=route_breakdown,
            meter_breakdown=meter_breakdown,
            currency=currency,
        )

        api_consumer_usage_quote_response.additional_properties = d
        return api_consumer_usage_quote_response

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
