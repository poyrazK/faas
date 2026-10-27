from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_profile_capture_request_kinds_item import (
    CreateProfileCaptureRequestKindsItem,
    check_create_profile_capture_request_kinds_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateProfileCaptureRequest")


@_attrs_define
class CreateProfileCaptureRequest:
    """On-demand capture request. Kinds defaults to [cpu]; duration_seconds to 10. Stacks is an instant snapshot of where
    every goroutine or thread is waiting, taken when the capture starts. Block and mutex are Go contention profiles
    (time goroutines spent blocked on channels or locks, or waiting for a mutex, during the window).

    """

    kinds: list[CreateProfileCaptureRequestKindsItem] | Unset = UNSET
    duration_seconds: int | Unset = 10
    instance_id: UUID | Unset = UNSET
    """Running instance to profile; empty selects the earliest-started RUNNING instance."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kinds: list[str] | Unset = UNSET
        if not isinstance(self.kinds, Unset):
            kinds = []
            for kinds_item_data in self.kinds:
                kinds_item: str = kinds_item_data
                kinds.append(kinds_item)

        duration_seconds = self.duration_seconds

        instance_id: str | Unset = UNSET
        if not isinstance(self.instance_id, Unset):
            instance_id = str(self.instance_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if kinds is not UNSET:
            field_dict["kinds"] = kinds
        if duration_seconds is not UNSET:
            field_dict["duration_seconds"] = duration_seconds
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _kinds = d.pop("kinds", UNSET)
        kinds: list[CreateProfileCaptureRequestKindsItem] | Unset = UNSET
        if _kinds is not UNSET:
            kinds = []
            for kinds_item_data in _kinds:
                kinds_item = check_create_profile_capture_request_kinds_item(kinds_item_data)

                kinds.append(kinds_item)

        duration_seconds = d.pop("duration_seconds", UNSET)

        _instance_id = d.pop("instance_id", UNSET)
        instance_id: UUID | Unset
        if isinstance(_instance_id, Unset):
            instance_id = UNSET
        else:
            instance_id = UUID(_instance_id)

        create_profile_capture_request = cls(
            kinds=kinds,
            duration_seconds=duration_seconds,
            instance_id=instance_id,
        )

        create_profile_capture_request.additional_properties = d
        return create_profile_capture_request

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
