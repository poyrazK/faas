from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_ai_access_token import OutboundAIAccessToken


T = TypeVar("T", bound="CreatedOutboundAIAccessToken")


@_attrs_define
class CreatedOutboundAIAccessToken:
    """A new token with its secret, which is shown only once."""

    access_token: OutboundAIAccessToken
    """An issued access token. prefix is the token's first 12 characters, to recognise it."""
    token: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        access_token = self.access_token.to_dict()

        token = self.token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "access_token": access_token,
                "token": token,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_access_token import OutboundAIAccessToken

        d = dict(src_dict)
        access_token = OutboundAIAccessToken.from_dict(d.pop("access_token"))

        token = d.pop("token")

        created_outbound_ai_access_token = cls(
            access_token=access_token,
            token=token,
        )

        created_outbound_ai_access_token.additional_properties = d
        return created_outbound_ai_access_token

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
