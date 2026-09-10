from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_app_log_drain_request_kind import (
    CreateAppLogDrainRequestKind,
    check_create_app_log_drain_request_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateAppLogDrainRequest")


@_attrs_define
class CreateAppLogDrainRequest:
    """Create a provider-neutral runtime log destination."""

    kind: CreateAppLogDrainRequestKind
    target_url: str
    auth_header: str | Unset = UNSET
    """One Name: value pair; sealed at rest and never returned."""
    enabled: bool | Unset = True
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        target_url = self.target_url

        auth_header = self.auth_header

        enabled = self.enabled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "target_url": target_url,
            }
        )
        if auth_header is not UNSET:
            field_dict["auth_header"] = auth_header
        if enabled is not UNSET:
            field_dict["enabled"] = enabled

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_create_app_log_drain_request_kind(d.pop("kind"))

        target_url = d.pop("target_url")

        auth_header = d.pop("auth_header", UNSET)

        enabled = d.pop("enabled", UNSET)

        create_app_log_drain_request = cls(
            kind=kind,
            target_url=target_url,
            auth_header=auth_header,
            enabled=enabled,
        )

        create_app_log_drain_request.additional_properties = d
        return create_app_log_drain_request

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
