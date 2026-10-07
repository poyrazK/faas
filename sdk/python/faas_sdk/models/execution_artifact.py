from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ExecutionArtifact")


@_attrs_define
class ExecutionArtifact:
    """An explicitly exported file. Its compact JSON metadata and base64 contents share the execution output budget."""

    name: str
    size_bytes: int
    sha256: str
    content: str
    """File bytes encoded as base64."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        size_bytes = self.size_bytes

        sha256 = self.sha256

        content = self.content

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "size_bytes": size_bytes,
                "sha256": sha256,
                "content": content,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        size_bytes = d.pop("size_bytes")

        sha256 = d.pop("sha256")

        content = d.pop("content")

        execution_artifact = cls(
            name=name,
            size_bytes=size_bytes,
            sha256=sha256,
            content=content,
        )

        return execution_artifact
