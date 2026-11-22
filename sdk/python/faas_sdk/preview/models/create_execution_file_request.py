from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="CreateExecutionFileRequest")


@_attrs_define
class CreateExecutionFileRequest:
    """Declared byte length and checksum for a prepared immutable Run input file."""

    size_bytes: int
    sha256: str

    def to_dict(self) -> dict[str, Any]:
        size_bytes = self.size_bytes

        sha256 = self.sha256

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "size_bytes": size_bytes,
                "sha256": sha256,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        size_bytes = d.pop("size_bytes")

        sha256 = d.pop("sha256")

        create_execution_file_request = cls(
            size_bytes=size_bytes,
            sha256=sha256,
        )

        return create_execution_file_request
