from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.billing_run_export import BillingRunExport, check_billing_run_export
from ..models.billing_run_sources_item import BillingRunSourcesItem, check_billing_run_sources_item
from ..types import UNSET, Unset

T = TypeVar("T", bound="PutBillingRunScheduleRequest")


@_attrs_define
class PutBillingRunScheduleRequest:
    """A monthly billing run schedule. Options default like a run's; delay_hours defaults to 24."""

    sources: list[BillingRunSourcesItem] | Unset = UNSET
    """Which customers a run bills. With platform_tenants, a consumer's tenant-attributed usage is billed on the
    tenant statement and its app item is skipped."""
    finalize: bool | Unset = UNSET
    """Finalize each drafted statement in scheduled runs (default true)."""
    export: BillingRunExport | Unset = UNSET
    """Where finalized statements go: none, or invoices in the account's Stripe, Paddle or Lago (ADR-1507; requires
    finalize and a connected key)."""
    hold_on_gaps: bool | Unset = UNSET
    """Hold app consumers with completeness gaps in scheduled runs (default true)."""
    delay_hours: int | Unset = UNSET
    """Hours after month end before the run starts (default 24)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sources: list[str] | Unset = UNSET
        if not isinstance(self.sources, Unset):
            sources = []
            for componentsschemas_billing_run_sources_item_data in self.sources:
                componentsschemas_billing_run_sources_item: str = componentsschemas_billing_run_sources_item_data
                sources.append(componentsschemas_billing_run_sources_item)

        finalize = self.finalize

        export: str | Unset = UNSET
        if not isinstance(self.export, Unset):
            export = self.export

        hold_on_gaps = self.hold_on_gaps

        delay_hours = self.delay_hours

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if sources is not UNSET:
            field_dict["sources"] = sources
        if finalize is not UNSET:
            field_dict["finalize"] = finalize
        if export is not UNSET:
            field_dict["export"] = export
        if hold_on_gaps is not UNSET:
            field_dict["hold_on_gaps"] = hold_on_gaps
        if delay_hours is not UNSET:
            field_dict["delay_hours"] = delay_hours

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _sources = d.pop("sources", UNSET)
        sources: list[BillingRunSourcesItem] | Unset = UNSET
        if _sources is not UNSET:
            sources = []
            for componentsschemas_billing_run_sources_item_data in _sources:
                componentsschemas_billing_run_sources_item = check_billing_run_sources_item(
                    componentsschemas_billing_run_sources_item_data
                )

                sources.append(componentsschemas_billing_run_sources_item)

        finalize = d.pop("finalize", UNSET)

        _export = d.pop("export", UNSET)
        export: BillingRunExport | Unset
        if isinstance(_export, Unset):
            export = UNSET
        else:
            export = check_billing_run_export(_export)

        hold_on_gaps = d.pop("hold_on_gaps", UNSET)

        delay_hours = d.pop("delay_hours", UNSET)

        put_billing_run_schedule_request = cls(
            sources=sources,
            finalize=finalize,
            export=export,
            hold_on_gaps=hold_on_gaps,
            delay_hours=delay_hours,
        )

        put_billing_run_schedule_request.additional_properties = d
        return put_billing_run_schedule_request

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
