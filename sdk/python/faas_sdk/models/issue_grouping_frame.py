from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="IssueGroupingFrame")


@_attrs_define
class IssueGroupingFrame:
    """Canonical file and function used in identity, with a zero-based index into selected cause frames when cause_index is
    present, otherwise occurrence frames. Line numbers are excluded.

    """

    frame_index: int
    file: str
    function: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        frame_index = self.frame_index

        file = self.file

        function = self.function

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "frame_index": frame_index,
                "file": file,
                "function": function,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        frame_index = d.pop("frame_index")

        file = d.pop("file")

        function = d.pop("function")

        issue_grouping_frame = cls(
            frame_index=frame_index,
            file=file,
            function=function,
        )

        issue_grouping_frame.additional_properties = d
        return issue_grouping_frame

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
