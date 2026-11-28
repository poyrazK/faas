from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.dev_bridge_session_summary_connection_state import (
    DevBridgeSessionSummaryConnectionState,
    check_dev_bridge_session_summary_connection_state,
)

if TYPE_CHECKING:
    from ..models.dev_bridge_session import DevBridgeSession


T = TypeVar("T", bound="DevBridgeSessionSummary")


@_attrs_define
class DevBridgeSessionSummary:
    """A durable session and its informational observed connection state."""

    session: DevBridgeSession
    """Durable session metadata with credential digests excluded."""
    connection_state: DevBridgeSessionSummaryConnectionState
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        session = self.session.to_dict()

        connection_state: str = self.connection_state

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "session": session,
                "connection_state": connection_state,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.dev_bridge_session import DevBridgeSession

        d = dict(src_dict)
        session = DevBridgeSession.from_dict(d.pop("session"))

        connection_state = check_dev_bridge_session_summary_connection_state(d.pop("connection_state"))

        dev_bridge_session_summary = cls(
            session=session,
            connection_state=connection_state,
        )

        dev_bridge_session_summary.additional_properties = d
        return dev_bridge_session_summary

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
