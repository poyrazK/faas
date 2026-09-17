from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdateManagedRealtimeEndpointRequest")


@_attrs_define
class UpdateManagedRealtimeEndpointRequest:
    """Partially update a managed realtime endpoint; omitted fields remain unchanged."""

    callback_url: str | Unset = UNSET
    connect_path: str | Unset = UNSET
    message_path: str | Unset = UNSET
    disconnect_path: str | Unset = UNSET
    callback_auth_token: str | Unset = UNSET
    auth_token: str | Unset = UNSET
    allowed_origins: list[str] | Unset = UNSET
    max_connections: int | Unset = UNSET
    max_message_bytes: int | Unset = UNSET
    max_connection_age_seconds: int | Unset = UNSET
    enabled: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        callback_url = self.callback_url

        connect_path = self.connect_path

        message_path = self.message_path

        disconnect_path = self.disconnect_path

        callback_auth_token = self.callback_auth_token

        auth_token = self.auth_token

        allowed_origins: list[str] | Unset = UNSET
        if not isinstance(self.allowed_origins, Unset):
            allowed_origins = self.allowed_origins

        max_connections = self.max_connections

        max_message_bytes = self.max_message_bytes

        max_connection_age_seconds = self.max_connection_age_seconds

        enabled = self.enabled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if callback_url is not UNSET:
            field_dict["callback_url"] = callback_url
        if connect_path is not UNSET:
            field_dict["connect_path"] = connect_path
        if message_path is not UNSET:
            field_dict["message_path"] = message_path
        if disconnect_path is not UNSET:
            field_dict["disconnect_path"] = disconnect_path
        if callback_auth_token is not UNSET:
            field_dict["callback_auth_token"] = callback_auth_token
        if auth_token is not UNSET:
            field_dict["auth_token"] = auth_token
        if allowed_origins is not UNSET:
            field_dict["allowed_origins"] = allowed_origins
        if max_connections is not UNSET:
            field_dict["max_connections"] = max_connections
        if max_message_bytes is not UNSET:
            field_dict["max_message_bytes"] = max_message_bytes
        if max_connection_age_seconds is not UNSET:
            field_dict["max_connection_age_seconds"] = max_connection_age_seconds
        if enabled is not UNSET:
            field_dict["enabled"] = enabled

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        callback_url = d.pop("callback_url", UNSET)

        connect_path = d.pop("connect_path", UNSET)

        message_path = d.pop("message_path", UNSET)

        disconnect_path = d.pop("disconnect_path", UNSET)

        callback_auth_token = d.pop("callback_auth_token", UNSET)

        auth_token = d.pop("auth_token", UNSET)

        allowed_origins = cast(list[str], d.pop("allowed_origins", UNSET))

        max_connections = d.pop("max_connections", UNSET)

        max_message_bytes = d.pop("max_message_bytes", UNSET)

        max_connection_age_seconds = d.pop("max_connection_age_seconds", UNSET)

        enabled = d.pop("enabled", UNSET)

        update_managed_realtime_endpoint_request = cls(
            callback_url=callback_url,
            connect_path=connect_path,
            message_path=message_path,
            disconnect_path=disconnect_path,
            callback_auth_token=callback_auth_token,
            auth_token=auth_token,
            allowed_origins=allowed_origins,
            max_connections=max_connections,
            max_message_bytes=max_message_bytes,
            max_connection_age_seconds=max_connection_age_seconds,
            enabled=enabled,
        )

        update_managed_realtime_endpoint_request.additional_properties = d
        return update_managed_realtime_endpoint_request

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
