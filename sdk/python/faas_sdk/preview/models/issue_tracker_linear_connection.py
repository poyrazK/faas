from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="IssueTrackerLinearConnection")


@_attrs_define
class IssueTrackerLinearConnection:
    """Non-secret owner-scoped Linear connection metadata; disconnected records omit provider identity."""

    connected: bool
    """Whether a verified API key or OAuth grant is stored."""
    revision: int
    """Configuration revision used to fence concurrent changes."""
    auth_method: str | Unset = UNSET
    """Stored credential kind (api_key or oauth)."""
    oauth_status: str | Unset = UNSET
    """OAuth health (ready, refreshing, reconnect_required or revoking); absent for API keys."""
    access_token_expires_at: datetime.datetime | Unset = UNSET
    """OAuth access token expiry; never includes credentials."""
    webhook_enabled: bool | Unset = UNSET
    """True when a scoped signing secret is stored for Linear issue callbacks."""
    callback_path: str | Unset = UNSET
    """Provider callback path to append to the Gregale API origin when callbacks are enabled."""
    workspace_id: UUID | Unset = UNSET
    """Verified Linear organization identity."""
    team_id: UUID | Unset = UNSET
    """Verified active Linear team identity."""
    viewer_id: UUID | Unset = UNSET
    """Verified Linear user identity owning the credential."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        connected = self.connected

        revision = self.revision

        auth_method = self.auth_method

        oauth_status = self.oauth_status

        access_token_expires_at: str | Unset = UNSET
        if not isinstance(self.access_token_expires_at, Unset):
            access_token_expires_at = self.access_token_expires_at.isoformat()

        webhook_enabled = self.webhook_enabled

        callback_path = self.callback_path

        workspace_id: str | Unset = UNSET
        if not isinstance(self.workspace_id, Unset):
            workspace_id = str(self.workspace_id)

        team_id: str | Unset = UNSET
        if not isinstance(self.team_id, Unset):
            team_id = str(self.team_id)

        viewer_id: str | Unset = UNSET
        if not isinstance(self.viewer_id, Unset):
            viewer_id = str(self.viewer_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "connected": connected,
                "revision": revision,
            }
        )
        if auth_method is not UNSET:
            field_dict["auth_method"] = auth_method
        if oauth_status is not UNSET:
            field_dict["oauth_status"] = oauth_status
        if access_token_expires_at is not UNSET:
            field_dict["access_token_expires_at"] = access_token_expires_at
        if webhook_enabled is not UNSET:
            field_dict["webhook_enabled"] = webhook_enabled
        if callback_path is not UNSET:
            field_dict["callback_path"] = callback_path
        if workspace_id is not UNSET:
            field_dict["workspace_id"] = workspace_id
        if team_id is not UNSET:
            field_dict["team_id"] = team_id
        if viewer_id is not UNSET:
            field_dict["viewer_id"] = viewer_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        connected = d.pop("connected")

        revision = d.pop("revision")

        auth_method = d.pop("auth_method", UNSET)

        oauth_status = d.pop("oauth_status", UNSET)

        _access_token_expires_at = d.pop("access_token_expires_at", UNSET)
        access_token_expires_at: datetime.datetime | Unset
        if isinstance(_access_token_expires_at, Unset):
            access_token_expires_at = UNSET
        else:
            access_token_expires_at = datetime.datetime.fromisoformat(_access_token_expires_at)

        webhook_enabled = d.pop("webhook_enabled", UNSET)

        callback_path = d.pop("callback_path", UNSET)

        _workspace_id = d.pop("workspace_id", UNSET)
        workspace_id: UUID | Unset
        if isinstance(_workspace_id, Unset):
            workspace_id = UNSET
        else:
            workspace_id = UUID(_workspace_id)

        _team_id = d.pop("team_id", UNSET)
        team_id: UUID | Unset
        if isinstance(_team_id, Unset):
            team_id = UNSET
        else:
            team_id = UUID(_team_id)

        _viewer_id = d.pop("viewer_id", UNSET)
        viewer_id: UUID | Unset
        if isinstance(_viewer_id, Unset):
            viewer_id = UNSET
        else:
            viewer_id = UUID(_viewer_id)

        issue_tracker_linear_connection = cls(
            connected=connected,
            revision=revision,
            auth_method=auth_method,
            oauth_status=oauth_status,
            access_token_expires_at=access_token_expires_at,
            webhook_enabled=webhook_enabled,
            callback_path=callback_path,
            workspace_id=workspace_id,
            team_id=team_id,
            viewer_id=viewer_id,
        )

        issue_tracker_linear_connection.additional_properties = d
        return issue_tracker_linear_connection

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
