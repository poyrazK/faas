from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_realtime_endpoint_response_auth_token_masked import (
    ManagedRealtimeEndpointResponseAuthTokenMasked,
    check_managed_realtime_endpoint_response_auth_token_masked,
)
from ..models.managed_realtime_endpoint_response_callback_auth_token_masked import (
    ManagedRealtimeEndpointResponseCallbackAuthTokenMasked,
    check_managed_realtime_endpoint_response_callback_auth_token_masked,
)

T = TypeVar("T", bound="ManagedRealtimeEndpointResponse")


@_attrs_define
class ManagedRealtimeEndpointResponse:
    """Durable managed realtime endpoint configuration. Credentials are
    write-only and represented by a constant mask on every response.

    """

    id: UUID
    app_id: UUID
    account_id: UUID
    callback_url: str
    connect_path: str
    message_path: str
    disconnect_path: str
    callback_auth_token_masked: ManagedRealtimeEndpointResponseCallbackAuthTokenMasked
    auth_token_masked: ManagedRealtimeEndpointResponseAuthTokenMasked
    enabled: bool
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        account_id = str(self.account_id)

        callback_url = self.callback_url

        connect_path = self.connect_path

        message_path = self.message_path

        disconnect_path = self.disconnect_path

        callback_auth_token_masked: str = self.callback_auth_token_masked

        auth_token_masked: str = self.auth_token_masked

        enabled = self.enabled

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "account_id": account_id,
                "callback_url": callback_url,
                "connect_path": connect_path,
                "message_path": message_path,
                "disconnect_path": disconnect_path,
                "callback_auth_token_masked": callback_auth_token_masked,
                "auth_token_masked": auth_token_masked,
                "enabled": enabled,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        account_id = UUID(d.pop("account_id"))

        callback_url = d.pop("callback_url")

        connect_path = d.pop("connect_path")

        message_path = d.pop("message_path")

        disconnect_path = d.pop("disconnect_path")

        callback_auth_token_masked = check_managed_realtime_endpoint_response_callback_auth_token_masked(
            d.pop("callback_auth_token_masked")
        )

        auth_token_masked = check_managed_realtime_endpoint_response_auth_token_masked(d.pop("auth_token_masked"))

        enabled = d.pop("enabled")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        managed_realtime_endpoint_response = cls(
            id=id,
            app_id=app_id,
            account_id=account_id,
            callback_url=callback_url,
            connect_path=connect_path,
            message_path=message_path,
            disconnect_path=disconnect_path,
            callback_auth_token_masked=callback_auth_token_masked,
            auth_token_masked=auth_token_masked,
            enabled=enabled,
            created_at=created_at,
            updated_at=updated_at,
        )

        managed_realtime_endpoint_response.additional_properties = d
        return managed_realtime_endpoint_response

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
