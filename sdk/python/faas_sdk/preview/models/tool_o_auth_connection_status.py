from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.tool_o_auth_connection_status_recovery_action import (
    ToolOAuthConnectionStatusRecoveryAction,
    check_tool_o_auth_connection_status_recovery_action,
)
from ..models.tool_o_auth_connection_status_state import (
    ToolOAuthConnectionStatusState,
    check_tool_o_auth_connection_status_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ToolOAuthConnectionStatus")


@_attrs_define
class ToolOAuthConnectionStatus:
    """Agent projection for one exact active invocation grant. Provider settings, secret references and credentials are
    excluded. Status is metadata, not proof of provider availability.

    """

    grant_id: UUID
    app: str
    app_id: str
    version: str
    name: str
    state: ToolOAuthConnectionStatusState
    expires_at: datetime.datetime | Unset = UNSET
    recovery_action: ToolOAuthConnectionStatusRecoveryAction | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        grant_id = str(self.grant_id)

        app = self.app

        app_id = self.app_id

        version = self.version

        name = self.name

        state: str = self.state

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        recovery_action: str | Unset = UNSET
        if not isinstance(self.recovery_action, Unset):
            recovery_action = self.recovery_action

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "grant_id": grant_id,
                "app": app,
                "app_id": app_id,
                "version": version,
                "name": name,
                "state": state,
            }
        )
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if recovery_action is not UNSET:
            field_dict["recovery_action"] = recovery_action

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        grant_id = UUID(d.pop("grant_id"))

        app = d.pop("app")

        app_id = d.pop("app_id")

        version = d.pop("version")

        name = d.pop("name")

        state = check_tool_o_auth_connection_status_state(d.pop("state"))

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        _recovery_action = d.pop("recovery_action", UNSET)
        recovery_action: ToolOAuthConnectionStatusRecoveryAction | Unset
        if isinstance(_recovery_action, Unset):
            recovery_action = UNSET
        else:
            recovery_action = check_tool_o_auth_connection_status_recovery_action(_recovery_action)

        tool_o_auth_connection_status = cls(
            grant_id=grant_id,
            app=app,
            app_id=app_id,
            version=version,
            name=name,
            state=state,
            expires_at=expires_at,
            recovery_action=recovery_action,
        )

        tool_o_auth_connection_status.additional_properties = d
        return tool_o_auth_connection_status

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
