from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="IssueSymbolicationResult")


@_attrs_define
class IssueSymbolicationResult:
    """Symbolication outcome and mapped frames for one reported exception in its deployment."""

    manifest_sha256: str
    mapped_frames: int
    unmapped_frames: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        manifest_sha256 = self.manifest_sha256

        mapped_frames = self.mapped_frames

        unmapped_frames = self.unmapped_frames

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "manifest_sha256": manifest_sha256,
                "mapped_frames": mapped_frames,
                "unmapped_frames": unmapped_frames,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        manifest_sha256 = d.pop("manifest_sha256")

        mapped_frames = d.pop("mapped_frames")

        unmapped_frames = d.pop("unmapped_frames")

        issue_symbolication_result = cls(
            manifest_sha256=manifest_sha256,
            mapped_frames=mapped_frames,
            unmapped_frames=unmapped_frames,
        )

        issue_symbolication_result.additional_properties = d
        return issue_symbolication_result

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
