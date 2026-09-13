from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.admin_status_event_update_request_state import (
    AdminStatusEventUpdateRequestState,
    check_admin_status_event_update_request_state,
)

T = TypeVar("T", bound="AdminStatusEventUpdateRequest")


@_attrs_define
class AdminStatusEventUpdateRequest:
    """Operator request to append a plain-text lifecycle update."""

    state: AdminStatusEventUpdateRequestState
    message: str

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        message = self.message

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "state": state,
                "message": message,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_admin_status_event_update_request_state(d.pop("state"))

        message = d.pop("message")

        admin_status_event_update_request = cls(
            state=state,
            message=message,
        )

        return admin_status_event_update_request
