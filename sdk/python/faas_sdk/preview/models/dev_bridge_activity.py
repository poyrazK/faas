from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.dev_bridge_activity_connection_state import (
    DevBridgeActivityConnectionState,
    check_dev_bridge_activity_connection_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.dev_bridge_request_record import DevBridgeRequestRecord


T = TypeVar("T", bound="DevBridgeActivity")


@_attrs_define
class DevBridgeActivity:
    """Temporary connection and request observations without credentials or payloads."""

    connection_state: DevBridgeActivityConnectionState
    requests: list[DevBridgeRequestRecord]
    connected_at: datetime.datetime | None | Unset = UNSET
    disconnected_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        connection_state: str = self.connection_state

        requests = []
        for requests_item_data in self.requests:
            requests_item = requests_item_data.to_dict()
            requests.append(requests_item)

        connected_at: None | str | Unset
        if isinstance(self.connected_at, Unset):
            connected_at = UNSET
        elif isinstance(self.connected_at, datetime.datetime):
            connected_at = self.connected_at.isoformat()
        else:
            connected_at = self.connected_at

        disconnected_at: None | str | Unset
        if isinstance(self.disconnected_at, Unset):
            disconnected_at = UNSET
        elif isinstance(self.disconnected_at, datetime.datetime):
            disconnected_at = self.disconnected_at.isoformat()
        else:
            disconnected_at = self.disconnected_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "connection_state": connection_state,
                "requests": requests,
            }
        )
        if connected_at is not UNSET:
            field_dict["connected_at"] = connected_at
        if disconnected_at is not UNSET:
            field_dict["disconnected_at"] = disconnected_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.dev_bridge_request_record import DevBridgeRequestRecord

        d = dict(src_dict)
        connection_state = check_dev_bridge_activity_connection_state(d.pop("connection_state"))

        requests = []
        _requests = d.pop("requests")
        for requests_item_data in _requests:
            requests_item = DevBridgeRequestRecord.from_dict(requests_item_data)

            requests.append(requests_item)

        def _parse_connected_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                connected_at_type_0 = datetime.datetime.fromisoformat(data)

                return connected_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        connected_at = _parse_connected_at(d.pop("connected_at", UNSET))

        def _parse_disconnected_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                disconnected_at_type_0 = datetime.datetime.fromisoformat(data)

                return disconnected_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        disconnected_at = _parse_disconnected_at(d.pop("disconnected_at", UNSET))

        dev_bridge_activity = cls(
            connection_state=connection_state,
            requests=requests,
            connected_at=connected_at,
            disconnected_at=disconnected_at,
        )

        dev_bridge_activity.additional_properties = d
        return dev_bridge_activity

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
