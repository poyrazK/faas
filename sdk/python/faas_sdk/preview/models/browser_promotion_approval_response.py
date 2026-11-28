from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="BrowserPromotionApprovalResponse")


@_attrs_define
class BrowserPromotionApprovalResponse:
    """Five-minute human review link with separate private polling material. Only review_url and expires_at may be exposed
    to agents.

    """

    review_url: str
    ticket: str
    exchange_code: str
    expires_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        review_url = self.review_url

        ticket = self.ticket

        exchange_code = self.exchange_code

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "review_url": review_url,
                "ticket": ticket,
                "exchange_code": exchange_code,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        review_url = d.pop("review_url")

        ticket = d.pop("ticket")

        exchange_code = d.pop("exchange_code")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        browser_promotion_approval_response = cls(
            review_url=review_url,
            ticket=ticket,
            exchange_code=exchange_code,
            expires_at=expires_at,
        )

        browser_promotion_approval_response.additional_properties = d
        return browser_promotion_approval_response

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
