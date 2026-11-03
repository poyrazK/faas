from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PutAPIConsumerTrialRequest")


@_attrs_define
class PutAPIConsumerTrialRequest:
    """A consumer's trial length in days from its start, or its end. Set exactly one."""

    days: int | Unset = UNSET
    ends_at: datetime.datetime | Unset = UNSET
    """A UTC minute from the next minute on."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        days = self.days

        ends_at: str | Unset = UNSET
        if not isinstance(self.ends_at, Unset):
            ends_at = self.ends_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if days is not UNSET:
            field_dict["days"] = days
        if ends_at is not UNSET:
            field_dict["ends_at"] = ends_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        days = d.pop("days", UNSET)

        _ends_at = d.pop("ends_at", UNSET)
        ends_at: datetime.datetime | Unset
        if isinstance(_ends_at, Unset):
            ends_at = UNSET
        else:
            ends_at = datetime.datetime.fromisoformat(_ends_at)

        put_api_consumer_trial_request = cls(
            days=days,
            ends_at=ends_at,
        )

        put_api_consumer_trial_request.additional_properties = d
        return put_api_consumer_trial_request

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
