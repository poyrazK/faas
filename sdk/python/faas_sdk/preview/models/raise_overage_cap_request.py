from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RaiseOverageCapRequest")


@_attrs_define
class RaiseOverageCapRequest:
    """Spend-cap payload (issue #561). *int64 so a missing/null
    field round-trips as NULL (no cap). 0 is a valid write and
    means "no overage allowed". Negative values are rejected at
    the validator before reaching the store (the migration CHECK
    at accounts/00049 is the storage-layer enforcement).

    """

    overage_cap_cents: int | None
    """Cents-per-month ceiling, or null to clear."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        overage_cap_cents: int | None
        overage_cap_cents = self.overage_cap_cents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "overage_cap_cents": overage_cap_cents,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_overage_cap_cents(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        overage_cap_cents = _parse_overage_cap_cents(d.pop("overage_cap_cents"))

        raise_overage_cap_request = cls(
            overage_cap_cents=overage_cap_cents,
        )

        raise_overage_cap_request.additional_properties = d
        return raise_overage_cap_request

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
