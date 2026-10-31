from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_capture_profile_kind import ProfileCaptureProfileKind, check_profile_capture_profile_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProfileCaptureProfile")


@_attrs_define
class ProfileCaptureProfile:
    """One collector's stored profile."""

    kind: ProfileCaptureProfileKind
    bytes_: int
    process_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        bytes_ = self.bytes_

        process_id = self.process_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "bytes": bytes_,
            }
        )
        if process_id is not UNSET:
            field_dict["process_id"] = process_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_profile_capture_profile_kind(d.pop("kind"))

        bytes_ = d.pop("bytes")

        process_id = d.pop("process_id", UNSET)

        profile_capture_profile = cls(
            kind=kind,
            bytes_=bytes_,
            process_id=process_id,
        )

        profile_capture_profile.additional_properties = d
        return profile_capture_profile

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
