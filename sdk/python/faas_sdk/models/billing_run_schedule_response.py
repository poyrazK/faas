from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.billing_run_export import BillingRunExport, check_billing_run_export
from ..models.billing_run_sources_item import BillingRunSourcesItem, check_billing_run_sources_item
from ..types import UNSET, Unset

T = TypeVar("T", bound="BillingRunScheduleResponse")


@_attrs_define
class BillingRunScheduleResponse:
    """The account's billing run schedule and the month it bills next."""

    sources: list[BillingRunSourcesItem]
    """Which customers a run bills. With platform_tenants, a consumer's tenant-attributed usage is billed on the
    tenant statement and its app item is skipped. product_customers bills each Product Control customer's statement
    for the month and needs Product Control to be enabled (ADR-20261018212918)."""
    finalize: bool
    export: BillingRunExport
    """Where finalized statements go: none, or invoices in the account's Stripe, Paddle or Lago (ADR-1507; requires
    finalize and a connected key)."""
    hold_on_gaps: bool
    delay_hours: int
    next_month: str
    next_run_at: datetime.datetime
    created_at: datetime.datetime
    updated_at: datetime.datetime
    last_month: str | Unset = UNSET
    """The last month the schedule started a run for."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sources = []
        for componentsschemas_billing_run_sources_item_data in self.sources:
            componentsschemas_billing_run_sources_item: str = componentsschemas_billing_run_sources_item_data
            sources.append(componentsschemas_billing_run_sources_item)

        finalize = self.finalize

        export: str = self.export

        hold_on_gaps = self.hold_on_gaps

        delay_hours = self.delay_hours

        next_month = self.next_month

        next_run_at = self.next_run_at.isoformat()

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        last_month = self.last_month

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sources": sources,
                "finalize": finalize,
                "export": export,
                "hold_on_gaps": hold_on_gaps,
                "delay_hours": delay_hours,
                "next_month": next_month,
                "next_run_at": next_run_at,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if last_month is not UNSET:
            field_dict["last_month"] = last_month

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sources = []
        _sources = d.pop("sources")
        for componentsschemas_billing_run_sources_item_data in _sources:
            componentsschemas_billing_run_sources_item = check_billing_run_sources_item(
                componentsschemas_billing_run_sources_item_data
            )

            sources.append(componentsschemas_billing_run_sources_item)

        finalize = d.pop("finalize")

        export = check_billing_run_export(d.pop("export"))

        hold_on_gaps = d.pop("hold_on_gaps")

        delay_hours = d.pop("delay_hours")

        next_month = d.pop("next_month")

        next_run_at = datetime.datetime.fromisoformat(d.pop("next_run_at"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        last_month = d.pop("last_month", UNSET)

        billing_run_schedule_response = cls(
            sources=sources,
            finalize=finalize,
            export=export,
            hold_on_gaps=hold_on_gaps,
            delay_hours=delay_hours,
            next_month=next_month,
            next_run_at=next_run_at,
            created_at=created_at,
            updated_at=updated_at,
            last_month=last_month,
        )

        billing_run_schedule_response.additional_properties = d
        return billing_run_schedule_response

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
