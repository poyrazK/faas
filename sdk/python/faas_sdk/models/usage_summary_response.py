from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.usage_summary_response_egress_billing_mode import (
    UsageSummaryResponseEgressBillingMode,
    check_usage_summary_response_egress_billing_mode,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.daily_usage_point import DailyUsagePoint


T = TypeVar("T", bound="UsageSummaryResponse")


@_attrs_define
class UsageSummaryResponse:
    """Account-level monthly roll-up: included GB-hours, used, overage math, remaining balance, informational usage
    dimensions, and a trailing 30-day daily trend (issue #308). The GB-hours fields drive the overage math; the other
    dimensions are informational.

    """

    month: str
    used_gb_hours: float
    included_gb_hours: int
    overage_gb_hours: float
    overage_cents: int
    """Integer cents. Includes compute at €0.01/GB-h and configured live egress overage; shadow egress is excluded."""
    used_cpu_hours: float | Unset = UNSET
    """Per-month CPU-hours (informational; not billed). issue #279 / PR-B."""
    used_egress_gb: float | Unset = UNSET
    """Per-month canonical interface egress GB. Σ net_tx_bytes across all apps; tx_bytes is a diagnostic subset and
    is not added. Informational while egress_billing_mode is absent."""
    egress_billing_mode: UsageSummaryResponseEgressBillingMode | Unset = UNSET
    """Absent when egress billing is off. Shadow quantities are audited locally but are not sent to Polar."""
    egress_billing_from: datetime.datetime | Unset = UNSET
    """Explicit non-retroactive UTC-hour activation boundary."""
    included_egress_gb: float | Unset = UNSET
    """UTC-calendar-month included egress in provider billing units (GiB for Polar). Present when egress billing is
    configured."""
    egress_overage_gb: float | Unset = UNSET
    """Current monthly egress above the included amount. Shadow values are not charged."""
    egress_millicents_per_gb: int | Unset = UNSET
    """Configured price per egress billing unit in millicents."""
    used_ingress_gb: float | Unset = UNSET
    """Per-month ingress GB (informational; not billed). Σ net_rx_bytes across all apps, converted to GB. ADR-048.
    Mirror of `used_egress_gb` for the inbound direction."""
    cold_boots: int | Unset = UNSET
    """Per-month sum of WAKE_RESTORE→WAKE_COLD_BOOT transitions across every app on the account (informational; not
    billed). ADR-048."""
    daily: list[DailyUsagePoint] | Unset = UNSET
    """Trailing 30 UTC calendar days, oldest first, grouped across the account. Empty when no daily rollup rows
    exist. issue #308."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        month = self.month

        used_gb_hours = self.used_gb_hours

        included_gb_hours = self.included_gb_hours

        overage_gb_hours = self.overage_gb_hours

        overage_cents = self.overage_cents

        used_cpu_hours = self.used_cpu_hours

        used_egress_gb = self.used_egress_gb

        egress_billing_mode: str | Unset = UNSET
        if not isinstance(self.egress_billing_mode, Unset):
            egress_billing_mode = self.egress_billing_mode

        egress_billing_from: str | Unset = UNSET
        if not isinstance(self.egress_billing_from, Unset):
            egress_billing_from = self.egress_billing_from.isoformat()

        included_egress_gb = self.included_egress_gb

        egress_overage_gb = self.egress_overage_gb

        egress_millicents_per_gb = self.egress_millicents_per_gb

        used_ingress_gb = self.used_ingress_gb

        cold_boots = self.cold_boots

        daily: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.daily, Unset):
            daily = []
            for daily_item_data in self.daily:
                daily_item = daily_item_data.to_dict()
                daily.append(daily_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "month": month,
                "used_gb_hours": used_gb_hours,
                "included_gb_hours": included_gb_hours,
                "overage_gb_hours": overage_gb_hours,
                "overage_cents": overage_cents,
            }
        )
        if used_cpu_hours is not UNSET:
            field_dict["used_cpu_hours"] = used_cpu_hours
        if used_egress_gb is not UNSET:
            field_dict["used_egress_gb"] = used_egress_gb
        if egress_billing_mode is not UNSET:
            field_dict["egress_billing_mode"] = egress_billing_mode
        if egress_billing_from is not UNSET:
            field_dict["egress_billing_from"] = egress_billing_from
        if included_egress_gb is not UNSET:
            field_dict["included_egress_gb"] = included_egress_gb
        if egress_overage_gb is not UNSET:
            field_dict["egress_overage_gb"] = egress_overage_gb
        if egress_millicents_per_gb is not UNSET:
            field_dict["egress_millicents_per_gb"] = egress_millicents_per_gb
        if used_ingress_gb is not UNSET:
            field_dict["used_ingress_gb"] = used_ingress_gb
        if cold_boots is not UNSET:
            field_dict["cold_boots"] = cold_boots
        if daily is not UNSET:
            field_dict["daily"] = daily

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.daily_usage_point import DailyUsagePoint

        d = dict(src_dict)
        month = d.pop("month")

        used_gb_hours = d.pop("used_gb_hours")

        included_gb_hours = d.pop("included_gb_hours")

        overage_gb_hours = d.pop("overage_gb_hours")

        overage_cents = d.pop("overage_cents")

        used_cpu_hours = d.pop("used_cpu_hours", UNSET)

        used_egress_gb = d.pop("used_egress_gb", UNSET)

        _egress_billing_mode = d.pop("egress_billing_mode", UNSET)
        egress_billing_mode: UsageSummaryResponseEgressBillingMode | Unset
        if isinstance(_egress_billing_mode, Unset):
            egress_billing_mode = UNSET
        else:
            egress_billing_mode = check_usage_summary_response_egress_billing_mode(_egress_billing_mode)

        _egress_billing_from = d.pop("egress_billing_from", UNSET)
        egress_billing_from: datetime.datetime | Unset
        if isinstance(_egress_billing_from, Unset):
            egress_billing_from = UNSET
        else:
            egress_billing_from = datetime.datetime.fromisoformat(_egress_billing_from)

        included_egress_gb = d.pop("included_egress_gb", UNSET)

        egress_overage_gb = d.pop("egress_overage_gb", UNSET)

        egress_millicents_per_gb = d.pop("egress_millicents_per_gb", UNSET)

        used_ingress_gb = d.pop("used_ingress_gb", UNSET)

        cold_boots = d.pop("cold_boots", UNSET)

        _daily = d.pop("daily", UNSET)
        daily: list[DailyUsagePoint] | Unset = UNSET
        if _daily is not UNSET:
            daily = []
            for daily_item_data in _daily:
                daily_item = DailyUsagePoint.from_dict(daily_item_data)

                daily.append(daily_item)

        usage_summary_response = cls(
            month=month,
            used_gb_hours=used_gb_hours,
            included_gb_hours=included_gb_hours,
            overage_gb_hours=overage_gb_hours,
            overage_cents=overage_cents,
            used_cpu_hours=used_cpu_hours,
            used_egress_gb=used_egress_gb,
            egress_billing_mode=egress_billing_mode,
            egress_billing_from=egress_billing_from,
            included_egress_gb=included_egress_gb,
            egress_overage_gb=egress_overage_gb,
            egress_millicents_per_gb=egress_millicents_per_gb,
            used_ingress_gb=used_ingress_gb,
            cold_boots=cold_boots,
            daily=daily,
        )

        usage_summary_response.additional_properties = d
        return usage_summary_response

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
