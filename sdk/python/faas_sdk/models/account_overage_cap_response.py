from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AccountOverageCapResponse")


@_attrs_define
class AccountOverageCapResponse:
    """Saved monthly overage ceiling in integer cents; null means no ceiling and zero disallows overage."""

    overage_cap_cents: int | None
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

        account_overage_cap_response = cls(
            overage_cap_cents=overage_cap_cents,
        )

        account_overage_cap_response.additional_properties = d
        return account_overage_cap_response

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
