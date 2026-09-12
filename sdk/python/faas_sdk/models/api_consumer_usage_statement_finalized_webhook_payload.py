from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_consumer_usage_statement_bucket_response import APIConsumerUsageStatementBucketResponse


T = TypeVar("T", bound="APIConsumerUsageStatementFinalizedWebhookPayload")


@_attrs_define
class APIConsumerUsageStatementFinalizedWebhookPayload:
    """Complete payable usage statement delivered with usage_statement.finalized."""

    app_id: UUID
    consumer_id: UUID
    statement_id: UUID
    period_start: datetime.datetime
    period_end: datetime.datetime
    billable_units: int
    unpriced_units: int
    amount_millicents: int
    priced: bool
    buckets: list[APIConsumerUsageStatementBucketResponse]
    as_of: datetime.datetime
    finalized_at: datetime.datetime
    currency: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        consumer_id = str(self.consumer_id)

        statement_id = str(self.statement_id)

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

        finalized_at = self.finalized_at.isoformat()

        currency = self.currency

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "consumer_id": consumer_id,
                "statement_id": statement_id,
                "period_start": period_start,
                "period_end": period_end,
                "billable_units": billable_units,
                "unpriced_units": unpriced_units,
                "amount_millicents": amount_millicents,
                "priced": priced,
                "buckets": buckets,
                "as_of": as_of,
                "finalized_at": finalized_at,
            }
        )
        if currency is not UNSET:
            field_dict["currency"] = currency

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_usage_statement_bucket_response import APIConsumerUsageStatementBucketResponse

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        consumer_id = UUID(d.pop("consumer_id"))

        statement_id = UUID(d.pop("statement_id"))

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

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

        finalized_at = datetime.datetime.fromisoformat(d.pop("finalized_at"))

        currency = d.pop("currency", UNSET)

        api_consumer_usage_statement_finalized_webhook_payload = cls(
            app_id=app_id,
            consumer_id=consumer_id,
            statement_id=statement_id,
            period_start=period_start,
            period_end=period_end,
            billable_units=billable_units,
            unpriced_units=unpriced_units,
            amount_millicents=amount_millicents,
            priced=priced,
            buckets=buckets,
            as_of=as_of,
            finalized_at=finalized_at,
            currency=currency,
        )

        api_consumer_usage_statement_finalized_webhook_payload.additional_properties = d
        return api_consumer_usage_statement_finalized_webhook_payload

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
