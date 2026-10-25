from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PaymentMethodSummary")


@_attrs_define
class PaymentMethodSummary:
    """Card-on-file summary (issue #242). Provider-agnostic shape —
    the same wire shape is returned whether the operator runs
    Stripe or Paddle. brand is the lowercase network label
    ("visa", "mastercard", "amex"); last4 is the last-4 of the
    PAN (no full PAN, no PCI surface); exp_month / exp_year are
    integer card-face expiry fields.

    """

    brand: str
    """Card network (lowercase)."""
    last4: str
    """Last 4 digits of the PAN."""
    exp_month: int
    """Card expiry month (1-12)."""
    exp_year: int
    """Card expiry year."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        brand = self.brand

        last4 = self.last4

        exp_month = self.exp_month

        exp_year = self.exp_year

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "brand": brand,
                "last4": last4,
                "exp_month": exp_month,
                "exp_year": exp_year,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        brand = d.pop("brand")

        last4 = d.pop("last4")

        exp_month = d.pop("exp_month")

        exp_year = d.pop("exp_year")

        payment_method_summary = cls(
            brand=brand,
            last4=last4,
            exp_month=exp_month,
            exp_year=exp_year,
        )

        payment_method_summary.additional_properties = d
        return payment_method_summary

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
