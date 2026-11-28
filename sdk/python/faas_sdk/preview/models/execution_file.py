from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ExecutionFile")


@_attrs_define
class ExecutionFile:
    """One regular file in an ephemeral execution bundle. Content is base64-encoded in JSON."""

    path: str
    """Normalized relative POSIX path; absolute paths, dot segments, and symlinks are rejected."""
    content: str
    """File bytes, base64-encoded by JSON clients."""

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        content = self.content

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "path": path,
                "content": content,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        path = d.pop("path")

        content = d.pop("content")

        execution_file = cls(
            path=path,
            content=content,
        )

        return execution_file
