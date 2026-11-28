from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="AdminStatusEventEditRequest")


@_attrs_define
class AdminStatusEventEditRequest:
    """Operator correction to a published event title."""

    title: str

    def to_dict(self) -> dict[str, Any]:
        title = self.title

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "title": title,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        title = d.pop("title")

        admin_status_event_edit_request = cls(
            title=title,
        )

        return admin_status_event_edit_request
