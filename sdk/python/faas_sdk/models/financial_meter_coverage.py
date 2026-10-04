from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="FinancialMeterCoverage")


@_attrs_define
class FinancialMeterCoverage:
    """Completeness and freshness of authoritative retained meter evidence."""

    complete: bool
    fresh: bool
    expected_minutes: int
    complete_minutes: int
    unpriced_quantity: int
    non_billable_quantity: int
    reasons: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        complete = self.complete

        fresh = self.fresh

        expected_minutes = self.expected_minutes

        complete_minutes = self.complete_minutes

        unpriced_quantity = self.unpriced_quantity

        non_billable_quantity = self.non_billable_quantity

        reasons = self.reasons

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "complete": complete,
                "fresh": fresh,
                "expected_minutes": expected_minutes,
                "complete_minutes": complete_minutes,
                "unpriced_quantity": unpriced_quantity,
                "non_billable_quantity": non_billable_quantity,
                "reasons": reasons,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        complete = d.pop("complete")

        fresh = d.pop("fresh")

        expected_minutes = d.pop("expected_minutes")

        complete_minutes = d.pop("complete_minutes")

        unpriced_quantity = d.pop("unpriced_quantity")

        non_billable_quantity = d.pop("non_billable_quantity")

        reasons = cast(list[str], d.pop("reasons"))

        financial_meter_coverage = cls(
            complete=complete,
            fresh=fresh,
            expected_minutes=expected_minutes,
            complete_minutes=complete_minutes,
            unpriced_quantity=unpriced_quantity,
            non_billable_quantity=non_billable_quantity,
            reasons=reasons,
        )

        financial_meter_coverage.additional_properties = d
        return financial_meter_coverage

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
