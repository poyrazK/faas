from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="BrowserPromotionApprovalExchange")


@_attrs_define
class BrowserPromotionApprovalExchange:
    """One-use exchange of a browser-authorized promotion ticket."""

    ticket: str
    exchange_code: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ticket = self.ticket

        exchange_code = self.exchange_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ticket": ticket,
                "exchange_code": exchange_code,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ticket = d.pop("ticket")

        exchange_code = d.pop("exchange_code")

        browser_promotion_approval_exchange = cls(
            ticket=ticket,
            exchange_code=exchange_code,
        )

        browser_promotion_approval_exchange.additional_properties = d
        return browser_promotion_approval_exchange

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
