from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdateIssueTrackerLinearConnectionRequest")


@_attrs_define
class UpdateIssueTrackerLinearConnectionRequest:
    """Explicit connection or disconnection against a known revision."""

    connected: bool
    """True connects a verified personal API key; false erases it."""
    expected_revision: int
    """Revision from the latest connection metadata read."""
    team_id: UUID | Unset = UNSET
    """Active team UUID required only when connecting."""
    api_key: str | Unset = UNSET
    """Personal API key required only when connecting; never returned or logged."""
    webhook_secret: str | Unset = UNSET
    """Optional Linear webhook signing secret stored with the connection. Omission on replacement disables
    callbacks. Never returned or logged."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        connected = self.connected

        expected_revision = self.expected_revision

        team_id: str | Unset = UNSET
        if not isinstance(self.team_id, Unset):
            team_id = str(self.team_id)

        api_key = self.api_key

        webhook_secret = self.webhook_secret

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "connected": connected,
                "expected_revision": expected_revision,
            }
        )
        if team_id is not UNSET:
            field_dict["team_id"] = team_id
        if api_key is not UNSET:
            field_dict["api_key"] = api_key
        if webhook_secret is not UNSET:
            field_dict["webhook_secret"] = webhook_secret

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        connected = d.pop("connected")

        expected_revision = d.pop("expected_revision")

        _team_id = d.pop("team_id", UNSET)
        team_id: UUID | Unset
        if isinstance(_team_id, Unset):
            team_id = UNSET
        else:
            team_id = UUID(_team_id)

        api_key = d.pop("api_key", UNSET)

        webhook_secret = d.pop("webhook_secret", UNSET)

        update_issue_tracker_linear_connection_request = cls(
            connected=connected,
            expected_revision=expected_revision,
            team_id=team_id,
            api_key=api_key,
            webhook_secret=webhook_secret,
        )

        update_issue_tracker_linear_connection_request.additional_properties = d
        return update_issue_tracker_linear_connection_request

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
