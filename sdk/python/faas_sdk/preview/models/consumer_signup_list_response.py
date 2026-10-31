from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.consumer_signup_response import ConsumerSignupResponse


T = TypeVar("T", bound="ConsumerSignupListResponse")


@_attrs_define
class ConsumerSignupListResponse:
    """An app's latest sign-ups, newest first."""

    signups: list[ConsumerSignupResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        signups = []
        for signups_item_data in self.signups:
            signups_item = signups_item_data.to_dict()
            signups.append(signups_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "signups": signups,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.consumer_signup_response import ConsumerSignupResponse

        d = dict(src_dict)
        signups = []
        _signups = d.pop("signups")
        for signups_item_data in _signups:
            signups_item = ConsumerSignupResponse.from_dict(signups_item_data)

            signups.append(signups_item)

        consumer_signup_list_response = cls(
            signups=signups,
        )

        consumer_signup_list_response.additional_properties = d
        return consumer_signup_list_response

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
