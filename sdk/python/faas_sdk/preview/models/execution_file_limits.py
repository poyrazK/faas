from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ExecutionFileLimits")


@_attrs_define
class ExecutionFileLimits:
    """Per-file and account storage limits for Run inputs and retained outputs."""

    max_file_bytes: int
    max_input_bytes: int
    max_output_bytes: int
    max_stored_bytes: int
    max_files: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        max_file_bytes = self.max_file_bytes

        max_input_bytes = self.max_input_bytes

        max_output_bytes = self.max_output_bytes

        max_stored_bytes = self.max_stored_bytes

        max_files = self.max_files

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "max_file_bytes": max_file_bytes,
                "max_input_bytes": max_input_bytes,
                "max_output_bytes": max_output_bytes,
                "max_stored_bytes": max_stored_bytes,
                "max_files": max_files,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_file_bytes = d.pop("max_file_bytes")

        max_input_bytes = d.pop("max_input_bytes")

        max_output_bytes = d.pop("max_output_bytes")

        max_stored_bytes = d.pop("max_stored_bytes")

        max_files = d.pop("max_files")

        execution_file_limits = cls(
            max_file_bytes=max_file_bytes,
            max_input_bytes=max_input_bytes,
            max_output_bytes=max_output_bytes,
            max_stored_bytes=max_stored_bytes,
            max_files=max_files,
        )

        execution_file_limits.additional_properties = d
        return execution_file_limits

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
