from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimeConnectionResponse")


@_attrs_define
class ManagedRealtimeConnectionResponse:
    """Safe control-plane projection of one live managed realtime connection."""

    id: UUID
    endpoint_id: UUID
    app_id: UUID
    account_id: UUID
    connected_at: datetime.datetime
    last_seen_at: datetime.datetime
    expires_at: datetime.datetime
    principal: str | Unset = UNSET
    channels: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        endpoint_id = str(self.endpoint_id)

        app_id = str(self.app_id)

        account_id = str(self.account_id)

        connected_at = self.connected_at.isoformat()

        last_seen_at = self.last_seen_at.isoformat()

        expires_at = self.expires_at.isoformat()

        principal = self.principal

        channels: list[str] | Unset = UNSET
        if not isinstance(self.channels, Unset):
            channels = self.channels

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "endpoint_id": endpoint_id,
                "app_id": app_id,
                "account_id": account_id,
                "connected_at": connected_at,
                "last_seen_at": last_seen_at,
                "expires_at": expires_at,
            }
        )
        if principal is not UNSET:
            field_dict["principal"] = principal
        if channels is not UNSET:
            field_dict["channels"] = channels

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        endpoint_id = UUID(d.pop("endpoint_id"))

        app_id = UUID(d.pop("app_id"))

        account_id = UUID(d.pop("account_id"))

        connected_at = datetime.datetime.fromisoformat(d.pop("connected_at"))

        last_seen_at = datetime.datetime.fromisoformat(d.pop("last_seen_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        principal = d.pop("principal", UNSET)

        channels = cast(list[str], d.pop("channels", UNSET))

        managed_realtime_connection_response = cls(
            id=id,
            endpoint_id=endpoint_id,
            app_id=app_id,
            account_id=account_id,
            connected_at=connected_at,
            last_seen_at=last_seen_at,
            expires_at=expires_at,
            principal=principal,
            channels=channels,
        )

        managed_realtime_connection_response.additional_properties = d
        return managed_realtime_connection_response

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
