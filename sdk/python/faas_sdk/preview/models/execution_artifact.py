from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ExecutionArtifact")


@_attrs_define
class ExecutionArtifact:
    """An explicitly exported file. Inline base64 content shares the JSON output budget. Stored artifacts contain a
    file_id, null content, and independently budgeted bytes accessible through authenticated file endpoints.

    """

    name: str
    size_bytes: int
    sha256: str
    content: None | str
    """File bytes encoded as base64."""
    file_id: UUID | Unset = UNSET
    """Authenticated retained file locator for stored artifacts."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        size_bytes = self.size_bytes

        sha256 = self.sha256

        content: None | str
        content = self.content

        file_id: str | Unset = UNSET
        if not isinstance(self.file_id, Unset):
            file_id = str(self.file_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "size_bytes": size_bytes,
                "sha256": sha256,
                "content": content,
            }
        )
        if file_id is not UNSET:
            field_dict["file_id"] = file_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        size_bytes = d.pop("size_bytes")

        sha256 = d.pop("sha256")

        def _parse_content(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        content = _parse_content(d.pop("content"))

        _file_id = d.pop("file_id", UNSET)
        file_id: UUID | Unset
        if isinstance(_file_id, Unset):
            file_id = UNSET
        else:
            file_id = UUID(_file_id)

        execution_artifact = cls(
            name=name,
            size_bytes=size_bytes,
            sha256=sha256,
            content=content,
            file_id=file_id,
        )

        return execution_artifact
