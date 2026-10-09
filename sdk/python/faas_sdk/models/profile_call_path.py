from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_call_path_view import ProfileCallPathView, check_profile_call_path_view

if TYPE_CHECKING:
    from ..models.profile_call_path_frame import ProfileCallPathFrame


T = TypeVar("T", bound="ProfileCallPath")


@_attrs_define
class ProfileCallPath:
    """Root-to-frame selection. Named comparison frames ignore sampled line changes; candidate and anonymous frames match
    their sampled line. Total name and file text is limited to 16384 UTF-8 bytes.

    """

    view: ProfileCallPathView
    frames: list[ProfileCallPathFrame]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        view: str = self.view

        frames = []
        for frames_item_data in self.frames:
            frames_item = frames_item_data.to_dict()
            frames.append(frames_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "view": view,
                "frames": frames,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_call_path_frame import ProfileCallPathFrame

        d = dict(src_dict)
        view = check_profile_call_path_view(d.pop("view"))

        frames = []
        _frames = d.pop("frames")
        for frames_item_data in _frames:
            frames_item = ProfileCallPathFrame.from_dict(frames_item_data)

            frames.append(frames_item)

        profile_call_path = cls(
            view=view,
            frames=frames,
        )

        profile_call_path.additional_properties = d
        return profile_call_path

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
