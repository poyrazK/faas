from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.hosted_task_source_entry_mode import HostedTaskSourceEntryMode, check_hosted_task_source_entry_mode
from ..types import UNSET, Unset

T = TypeVar("T", bound="HostedTaskSourceEntry")


@_attrs_define
class HostedTaskSourceEntry:
    """Authored regular file or explicit directory. Normalized paths, root directory, private modes and 2 MiB/1024 entries
    per role are mandatory. Content is base64-encoded bytes, accepted only by prepare and never returned.

    """

    path: str
    mode: HostedTaskSourceEntryMode
    directory: bool | Unset = UNSET
    content: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        mode: int = self.mode

        directory = self.directory

        content = self.content

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "path": path,
                "mode": mode,
            }
        )
        if directory is not UNSET:
            field_dict["directory"] = directory
        if content is not UNSET:
            field_dict["content"] = content

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        path = d.pop("path")

        mode = check_hosted_task_source_entry_mode(d.pop("mode"))

        directory = d.pop("directory", UNSET)

        content = d.pop("content", UNSET)

        hosted_task_source_entry = cls(
            path=path,
            mode=mode,
            directory=directory,
            content=content,
        )

        return hosted_task_source_entry
