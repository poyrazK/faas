from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="AdminStatusUpdateEditRequest")


@_attrs_define
class AdminStatusUpdateEditRequest:
    """Operator correction to a published timeline message."""

    message: str

    def to_dict(self) -> dict[str, Any]:
        message = self.message

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "message": message,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        message = d.pop("message")

        admin_status_update_edit_request = cls(
            message=message,
        )

        return admin_status_update_edit_request
