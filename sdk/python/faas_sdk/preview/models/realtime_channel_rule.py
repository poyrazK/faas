from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimeChannelRule")


@_attrs_define
class RealtimeChannelRule:
    """Grants a channel to a verified token without the authorize callback. The pattern is literal text with {claim}
    placeholders that must equal the token's claims and an optional trailing * matching any suffix.

    """

    pattern: str
    read: bool
    """Must be true: a rule grants read access."""
    presence: bool | Unset = UNSET
    """Also grants setting presence state and sending signals on the channel. Without it those go to the authorize
    callback with permission presence."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        pattern = self.pattern

        read = self.read

        presence = self.presence

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "pattern": pattern,
                "read": read,
            }
        )
        if presence is not UNSET:
            field_dict["presence"] = presence

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        pattern = d.pop("pattern")

        read = d.pop("read")

        presence = d.pop("presence", UNSET)

        realtime_channel_rule = cls(
            pattern=pattern,
            read=read,
            presence=presence,
        )

        realtime_channel_rule.additional_properties = d
        return realtime_channel_rule

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
