from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.financial_costs_response_currency import (
    FinancialCostsResponseCurrency,
    check_financial_costs_response_currency,
)
from ..models.financial_costs_response_invoice_reconciliation import (
    FinancialCostsResponseInvoiceReconciliation,
    check_financial_costs_response_invoice_reconciliation,
)

if TYPE_CHECKING:
    from ..models.financial_meter_costs import FinancialMeterCosts
    from ..models.invoice import Invoice


T = TypeVar("T", bound="FinancialCostsResponse")


@_attrs_define
class FinancialCostsResponse:
    """Account usage costs for a UTC period; invoice facts remain separate."""

    account_id: str
    currency: FinancialCostsResponseCurrency
    period_start: datetime.datetime
    period_end: datetime.datetime
    as_of: datetime.datetime
    retained_from: datetime.datetime
    evidence_through_id: int
    known_usage_millicents: int
    meters: list[FinancialMeterCosts]
    scope: str
    invoices: list[Invoice]
    invoice_reconciliation: FinancialCostsResponseInvoiceReconciliation
    missing_bill_components: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = self.account_id

        currency: str = self.currency

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        as_of = self.as_of.isoformat()

        retained_from = self.retained_from.isoformat()

        evidence_through_id = self.evidence_through_id

        known_usage_millicents = self.known_usage_millicents

        meters = []
        for meters_item_data in self.meters:
            meters_item = meters_item_data.to_dict()
            meters.append(meters_item)

        scope = self.scope

        invoices = []
        for invoices_item_data in self.invoices:
            invoices_item = invoices_item_data.to_dict()
            invoices.append(invoices_item)

        invoice_reconciliation: str = self.invoice_reconciliation

        missing_bill_components = self.missing_bill_components

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "currency": currency,
                "period_start": period_start,
                "period_end": period_end,
                "as_of": as_of,
                "retained_from": retained_from,
                "evidence_through_id": evidence_through_id,
                "known_usage_millicents": known_usage_millicents,
                "meters": meters,
                "scope": scope,
                "invoices": invoices,
                "invoice_reconciliation": invoice_reconciliation,
                "missing_bill_components": missing_bill_components,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_meter_costs import FinancialMeterCosts
        from ..models.invoice import Invoice

        d = dict(src_dict)
        account_id = d.pop("account_id")

        currency = check_financial_costs_response_currency(d.pop("currency"))

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        retained_from = datetime.datetime.fromisoformat(d.pop("retained_from"))

        evidence_through_id = d.pop("evidence_through_id")

        known_usage_millicents = d.pop("known_usage_millicents")

        meters = []
        _meters = d.pop("meters")
        for meters_item_data in _meters:
            meters_item = FinancialMeterCosts.from_dict(meters_item_data)

            meters.append(meters_item)

        scope = d.pop("scope")

        invoices = []
        _invoices = d.pop("invoices")
        for invoices_item_data in _invoices:
            invoices_item = Invoice.from_dict(invoices_item_data)

            invoices.append(invoices_item)

        invoice_reconciliation = check_financial_costs_response_invoice_reconciliation(d.pop("invoice_reconciliation"))

        missing_bill_components = cast(list[str], d.pop("missing_bill_components"))

        financial_costs_response = cls(
            account_id=account_id,
            currency=currency,
            period_start=period_start,
            period_end=period_end,
            as_of=as_of,
            retained_from=retained_from,
            evidence_through_id=evidence_through_id,
            known_usage_millicents=known_usage_millicents,
            meters=meters,
            scope=scope,
            invoices=invoices,
            invoice_reconciliation=invoice_reconciliation,
            missing_bill_components=missing_bill_components,
        )

        financial_costs_response.additional_properties = d
        return financial_costs_response

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
