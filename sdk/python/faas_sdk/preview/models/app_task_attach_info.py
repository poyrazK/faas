from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_task_attach_info_subprotocol import (
    AppTaskAttachInfoSubprotocol,
    check_app_task_attach_info_subprotocol,
)

T = TypeVar("T", bound="AppTaskAttachInfo")


@_attrs_define
class AppTaskAttachInfo:
    """One-time attach credential for an interactive app task. Returned only in the create response."""

    token: str
    """Send in X-Gregale-Attach-Token with the account credential; consumed by the first attach."""
    path: str
    """Gateway WebSocket path for this task."""
    subprotocol: AppTaskAttachInfoSubprotocol
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        token = self.token

        path = self.path

        subprotocol: str = self.subprotocol

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "token": token,
                "path": path,
                "subprotocol": subprotocol,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        token = d.pop("token")

        path = d.pop("path")

        subprotocol = check_app_task_attach_info_subprotocol(d.pop("subprotocol"))

        app_task_attach_info = cls(
            token=token,
            path=path,
            subprotocol=subprotocol,
        )

        app_task_attach_info.additional_properties = d
        return app_task_attach_info

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
