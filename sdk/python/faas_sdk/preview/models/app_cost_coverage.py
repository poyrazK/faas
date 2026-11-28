from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AppCostCoverage")


@_attrs_define
class AppCostCoverage:
    """Source sampling windows with per-app quantity coverage explicitly unknown."""

    complete: bool
    fresh: bool
    expected_minutes: int
    complete_minutes: int
    quantity_coverage_known: bool
    """False; account-wide unpriced and nonbillable quantities are not exposed."""
    reasons: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        complete = self.complete

        fresh = self.fresh

        expected_minutes = self.expected_minutes

        complete_minutes = self.complete_minutes

        quantity_coverage_known = self.quantity_coverage_known

        reasons = self.reasons

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "complete": complete,
                "fresh": fresh,
                "expected_minutes": expected_minutes,
                "complete_minutes": complete_minutes,
                "quantity_coverage_known": quantity_coverage_known,
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

        quantity_coverage_known = d.pop("quantity_coverage_known")

        reasons = cast(list[str], d.pop("reasons"))

        app_cost_coverage = cls(
            complete=complete,
            fresh=fresh,
            expected_minutes=expected_minutes,
            complete_minutes=complete_minutes,
            quantity_coverage_known=quantity_coverage_known,
            reasons=reasons,
        )

        app_cost_coverage.additional_properties = d
        return app_cost_coverage

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
