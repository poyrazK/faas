from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateAPIConsumerRequest")


@_attrs_define
class CreateAPIConsumerRequest:
    """Stable customer identity to which consumer credentials can be attached."""

    external_ref: str
    name: str
    trial_days: int | Unset = UNSET
    """Starts the consumer's one free trial at the next UTC minute for this many days."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        external_ref = self.external_ref

        name = self.name

        trial_days = self.trial_days

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "external_ref": external_ref,
                "name": name,
            }
        )
        if trial_days is not UNSET:
            field_dict["trial_days"] = trial_days

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        external_ref = d.pop("external_ref")

        name = d.pop("name")

        trial_days = d.pop("trial_days", UNSET)

        create_api_consumer_request = cls(
            external_ref=external_ref,
            name=name,
            trial_days=trial_days,
        )

        create_api_consumer_request.additional_properties = d
        return create_api_consumer_request

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
