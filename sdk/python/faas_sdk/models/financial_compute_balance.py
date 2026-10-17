from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="FinancialComputeBalance")


@_attrs_define
class FinancialComputeBalance:
    """Conservative app admission ledger before allowances, including startup and rounding per funded minute. These
    commitments may exceed invoice compute charges. Expired funding stays reserved until physical retirement and
    settlement. Other meters, subscriptions and taxes are excluded.

    """

    initialized: bool
    app_id: UUID
    period_start: datetime.datetime
    period_end: datetime.datetime
    committed_millicents: int
    reserved_millicents: int
    remaining_millicents: int
    unconfirmed_leases: int
    held: bool
    observed_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        initialized = self.initialized

        app_id = str(self.app_id)

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        committed_millicents = self.committed_millicents

        reserved_millicents = self.reserved_millicents

        remaining_millicents = self.remaining_millicents

        unconfirmed_leases = self.unconfirmed_leases

        held = self.held

        observed_at = self.observed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "initialized": initialized,
                "app_id": app_id,
                "period_start": period_start,
                "period_end": period_end,
                "committed_millicents": committed_millicents,
                "reserved_millicents": reserved_millicents,
                "remaining_millicents": remaining_millicents,
                "unconfirmed_leases": unconfirmed_leases,
                "held": held,
                "observed_at": observed_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        initialized = d.pop("initialized")

        app_id = UUID(d.pop("app_id"))

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        committed_millicents = d.pop("committed_millicents")

        reserved_millicents = d.pop("reserved_millicents")

        remaining_millicents = d.pop("remaining_millicents")

        unconfirmed_leases = d.pop("unconfirmed_leases")

        held = d.pop("held")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        financial_compute_balance = cls(
            initialized=initialized,
            app_id=app_id,
            period_start=period_start,
            period_end=period_end,
            committed_millicents=committed_millicents,
            reserved_millicents=reserved_millicents,
            remaining_millicents=remaining_millicents,
            unconfirmed_leases=unconfirmed_leases,
            held=held,
            observed_at=observed_at,
        )

        financial_compute_balance.additional_properties = d
        return financial_compute_balance

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
