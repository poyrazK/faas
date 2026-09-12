from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_consumer_usage_statement_response_status import (
    APIConsumerUsageStatementResponseStatus,
    check_api_consumer_usage_statement_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_consumer_usage_statement_bucket_response import APIConsumerUsageStatementBucketResponse


T = TypeVar("T", bound="APIConsumerUsageStatementResponse")


@_attrs_define
class APIConsumerUsageStatementResponse:
    """Immutable, auditable API consumer usage snapshot."""

    id: UUID
    consumer_id: UUID
    period_start: datetime.datetime
    period_end: datetime.datetime
    status: APIConsumerUsageStatementResponseStatus
    billable_units: int
    unpriced_units: int
    amount_millicents: int
    priced: bool
    buckets: list[APIConsumerUsageStatementBucketResponse]
    as_of: datetime.datetime
    created_at: datetime.datetime
    currency: str | Unset = UNSET
    finalized_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        consumer_id = str(self.consumer_id)

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        status: str = self.status

        billable_units = self.billable_units

        unpriced_units = self.unpriced_units

        amount_millicents = self.amount_millicents

        priced = self.priced

        buckets = []
        for buckets_item_data in self.buckets:
            buckets_item = buckets_item_data.to_dict()
            buckets.append(buckets_item)

        as_of = self.as_of.isoformat()

        created_at = self.created_at.isoformat()

        currency = self.currency

        finalized_at: None | str | Unset
        if isinstance(self.finalized_at, Unset):
            finalized_at = UNSET
        elif isinstance(self.finalized_at, datetime.datetime):
            finalized_at = self.finalized_at.isoformat()
        else:
            finalized_at = self.finalized_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "consumer_id": consumer_id,
                "period_start": period_start,
                "period_end": period_end,
                "status": status,
                "billable_units": billable_units,
                "unpriced_units": unpriced_units,
                "amount_millicents": amount_millicents,
                "priced": priced,
                "buckets": buckets,
                "as_of": as_of,
                "created_at": created_at,
            }
        )
        if currency is not UNSET:
            field_dict["currency"] = currency
        if finalized_at is not UNSET:
            field_dict["finalized_at"] = finalized_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_usage_statement_bucket_response import APIConsumerUsageStatementBucketResponse

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        consumer_id = UUID(d.pop("consumer_id"))

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        status = check_api_consumer_usage_statement_response_status(d.pop("status"))

        billable_units = d.pop("billable_units")

        unpriced_units = d.pop("unpriced_units")

        amount_millicents = d.pop("amount_millicents")

        priced = d.pop("priced")

        buckets = []
        _buckets = d.pop("buckets")
        for buckets_item_data in _buckets:
            buckets_item = APIConsumerUsageStatementBucketResponse.from_dict(buckets_item_data)

            buckets.append(buckets_item)

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        currency = d.pop("currency", UNSET)

        def _parse_finalized_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                finalized_at_type_0 = datetime.datetime.fromisoformat(data)

                return finalized_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        finalized_at = _parse_finalized_at(d.pop("finalized_at", UNSET))

        api_consumer_usage_statement_response = cls(
            id=id,
            consumer_id=consumer_id,
            period_start=period_start,
            period_end=period_end,
            status=status,
            billable_units=billable_units,
            unpriced_units=unpriced_units,
            amount_millicents=amount_millicents,
            priced=priced,
            buckets=buckets,
            as_of=as_of,
            created_at=created_at,
            currency=currency,
            finalized_at=finalized_at,
        )

        api_consumer_usage_statement_response.additional_properties = d
        return api_consumer_usage_statement_response

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
