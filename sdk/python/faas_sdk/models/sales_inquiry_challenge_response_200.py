from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="SalesInquiryChallengeResponse200")


@_attrs_define
class SalesInquiryChallengeResponse200:
    challenge: str
    ready_after_ms: int
    expires_in_seconds: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        challenge = self.challenge

        ready_after_ms = self.ready_after_ms

        expires_in_seconds = self.expires_in_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "challenge": challenge,
                "ready_after_ms": ready_after_ms,
                "expires_in_seconds": expires_in_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        challenge = d.pop("challenge")

        ready_after_ms = d.pop("ready_after_ms")

        expires_in_seconds = d.pop("expires_in_seconds")

        sales_inquiry_challenge_response_200 = cls(
            challenge=challenge,
            ready_after_ms=ready_after_ms,
            expires_in_seconds=expires_in_seconds,
        )

        sales_inquiry_challenge_response_200.additional_properties = d
        return sales_inquiry_challenge_response_200

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
