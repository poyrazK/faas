from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.update_app_log_drain_request_kind import (
    UpdateAppLogDrainRequestKind,
    check_update_app_log_drain_request_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdateAppLogDrainRequest")


@_attrs_define
class UpdateAppLogDrainRequest:
    """Partially update a runtime log destination; omitted fields remain unchanged."""

    kind: UpdateAppLogDrainRequestKind | Unset = UNSET
    target_url: str | Unset = UNSET
    auth_header: str | Unset = UNSET
    """One Name: value pair; an empty value clears credentials."""
    enabled: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str | Unset = UNSET
        if not isinstance(self.kind, Unset):
            kind = self.kind

        target_url = self.target_url

        auth_header = self.auth_header

        enabled = self.enabled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if kind is not UNSET:
            field_dict["kind"] = kind
        if target_url is not UNSET:
            field_dict["target_url"] = target_url
        if auth_header is not UNSET:
            field_dict["auth_header"] = auth_header
        if enabled is not UNSET:
            field_dict["enabled"] = enabled

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _kind = d.pop("kind", UNSET)
        kind: UpdateAppLogDrainRequestKind | Unset
        if isinstance(_kind, Unset):
            kind = UNSET
        else:
            kind = check_update_app_log_drain_request_kind(_kind)

        target_url = d.pop("target_url", UNSET)

        auth_header = d.pop("auth_header", UNSET)

        enabled = d.pop("enabled", UNSET)

        update_app_log_drain_request = cls(
            kind=kind,
            target_url=target_url,
            auth_header=auth_header,
            enabled=enabled,
        )

        update_app_log_drain_request.additional_properties = d
        return update_app_log_drain_request

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
