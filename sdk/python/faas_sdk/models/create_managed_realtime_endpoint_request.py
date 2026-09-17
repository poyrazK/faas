from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateManagedRealtimeEndpointRequest")


@_attrs_define
class CreateManagedRealtimeEndpointRequest:
    """Create a durable managed realtime endpoint and its callback contract."""

    callback_url: str
    callback_auth_token: str
    connect_path: str | Unset = "/realtime/connect"
    message_path: str | Unset = "/realtime/message"
    disconnect_path: str | Unset = "/realtime/disconnect"
    auth_token: str | Unset = UNSET
    allowed_origins: list[str] | Unset = UNSET
    max_connections: int | Unset = 0
    max_message_bytes: int | Unset = 0
    max_connection_age_seconds: int | Unset = 0
    enabled: bool | Unset = True
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        callback_url = self.callback_url

        callback_auth_token = self.callback_auth_token

        connect_path = self.connect_path

        message_path = self.message_path

        disconnect_path = self.disconnect_path

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
        field_dict.update(
            {
                "callback_url": callback_url,
                "callback_auth_token": callback_auth_token,
            }
        )
        if connect_path is not UNSET:
            field_dict["connect_path"] = connect_path
        if message_path is not UNSET:
            field_dict["message_path"] = message_path
        if disconnect_path is not UNSET:
            field_dict["disconnect_path"] = disconnect_path
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
        callback_url = d.pop("callback_url")

        callback_auth_token = d.pop("callback_auth_token")

        connect_path = d.pop("connect_path", UNSET)

        message_path = d.pop("message_path", UNSET)

        disconnect_path = d.pop("disconnect_path", UNSET)

        auth_token = d.pop("auth_token", UNSET)

        allowed_origins = cast(list[str], d.pop("allowed_origins", UNSET))

        max_connections = d.pop("max_connections", UNSET)

        max_message_bytes = d.pop("max_message_bytes", UNSET)

        max_connection_age_seconds = d.pop("max_connection_age_seconds", UNSET)

        enabled = d.pop("enabled", UNSET)

        create_managed_realtime_endpoint_request = cls(
            callback_url=callback_url,
            callback_auth_token=callback_auth_token,
            connect_path=connect_path,
            message_path=message_path,
            disconnect_path=disconnect_path,
            auth_token=auth_token,
            allowed_origins=allowed_origins,
            max_connections=max_connections,
            max_message_bytes=max_message_bytes,
            max_connection_age_seconds=max_connection_age_seconds,
            enabled=enabled,
        )

        create_managed_realtime_endpoint_request.additional_properties = d
        return create_managed_realtime_endpoint_request

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
