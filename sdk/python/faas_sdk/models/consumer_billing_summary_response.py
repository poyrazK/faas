from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_consumer_credit_balance import APIConsumerCreditBalance
    from ..models.consumer_billing_plan import ConsumerBillingPlan
    from ..models.consumer_billing_spend_cap import ConsumerBillingSpendCap


T = TypeVar("T", bound="ConsumerBillingSummaryResponse")


@_attrs_define
class ConsumerBillingSummaryResponse:
    """A consumer's own month to date (ADR-1506), priced like its next statement."""

    consumer_id: UUID
    external_ref: str
    name: str
    app: str
    """The app's slug."""
    month: str
    billable_units: int
    unpriced_units: int
    amount_millicents: int
    """Month-to-date amount; 100000 millicents is 1.00."""
    priced: bool
    """False while some usage has no effective rate card."""
    as_of: datetime.datetime
    currency: str | Unset = UNSET
    plan: ConsumerBillingPlan | Unset = UNSET
    """The consumer's current plan and its monthly unit cap."""
    credits_: list[APIConsumerCreditBalance] | Unset = UNSET
    """Prepaid credit the consumer can still use, per currency (ADR-1306)."""
    spend_cap: ConsumerBillingSpendCap | Unset = UNSET
    """The consumer's monthly spend cap; once the amount due passes it, requests are refused until the month ends
    or credit is added."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        consumer_id = str(self.consumer_id)

        external_ref = self.external_ref

        name = self.name

        app = self.app

        month = self.month

        billable_units = self.billable_units

        unpriced_units = self.unpriced_units

        amount_millicents = self.amount_millicents

        priced = self.priced

        as_of = self.as_of.isoformat()

        currency = self.currency

        plan: dict[str, Any] | Unset = UNSET
        if not isinstance(self.plan, Unset):
            plan = self.plan.to_dict()

        credits_: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.credits_, Unset):
            credits_ = []
            for credits_item_data in self.credits_:
                credits_item = credits_item_data.to_dict()
                credits_.append(credits_item)

        spend_cap: dict[str, Any] | Unset = UNSET
        if not isinstance(self.spend_cap, Unset):
            spend_cap = self.spend_cap.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "consumer_id": consumer_id,
                "external_ref": external_ref,
                "name": name,
                "app": app,
                "month": month,
                "billable_units": billable_units,
                "unpriced_units": unpriced_units,
                "amount_millicents": amount_millicents,
                "priced": priced,
                "as_of": as_of,
            }
        )
        if currency is not UNSET:
            field_dict["currency"] = currency
        if plan is not UNSET:
            field_dict["plan"] = plan
        if credits_ is not UNSET:
            field_dict["credits"] = credits_
        if spend_cap is not UNSET:
            field_dict["spend_cap"] = spend_cap

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_credit_balance import APIConsumerCreditBalance
        from ..models.consumer_billing_plan import ConsumerBillingPlan
        from ..models.consumer_billing_spend_cap import ConsumerBillingSpendCap

        d = dict(src_dict)
        consumer_id = UUID(d.pop("consumer_id"))

        external_ref = d.pop("external_ref")

        name = d.pop("name")

        app = d.pop("app")

        month = d.pop("month")

        billable_units = d.pop("billable_units")

        unpriced_units = d.pop("unpriced_units")

        amount_millicents = d.pop("amount_millicents")

        priced = d.pop("priced")

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        currency = d.pop("currency", UNSET)

        _plan = d.pop("plan", UNSET)
        plan: ConsumerBillingPlan | Unset
        if isinstance(_plan, Unset):
            plan = UNSET
        else:
            plan = ConsumerBillingPlan.from_dict(_plan)

        _credits_ = d.pop("credits", UNSET)
        credits_: list[APIConsumerCreditBalance] | Unset = UNSET
        if _credits_ is not UNSET:
            credits_ = []
            for credits_item_data in _credits_:
                credits_item = APIConsumerCreditBalance.from_dict(credits_item_data)

                credits_.append(credits_item)

        _spend_cap = d.pop("spend_cap", UNSET)
        spend_cap: ConsumerBillingSpendCap | Unset
        if isinstance(_spend_cap, Unset):
            spend_cap = UNSET
        else:
            spend_cap = ConsumerBillingSpendCap.from_dict(_spend_cap)

        consumer_billing_summary_response = cls(
            consumer_id=consumer_id,
            external_ref=external_ref,
            name=name,
            app=app,
            month=month,
            billable_units=billable_units,
            unpriced_units=unpriced_units,
            amount_millicents=amount_millicents,
            priced=priced,
            as_of=as_of,
            currency=currency,
            plan=plan,
            credits_=credits_,
            spend_cap=spend_cap,
        )

        consumer_billing_summary_response.additional_properties = d
        return consumer_billing_summary_response

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
