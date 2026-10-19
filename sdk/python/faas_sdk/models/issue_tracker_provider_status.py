from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.issue_tracker_provider_status_provider import (
    IssueTrackerProviderStatusProvider,
    check_issue_tracker_provider_status_provider,
)
from ..models.issue_tracker_provider_status_state import (
    IssueTrackerProviderStatusState,
    check_issue_tracker_provider_status_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="IssueTrackerProviderStatus")


@_attrs_define
class IssueTrackerProviderStatus:
    """Observed lifecycle and identity of an external tracker issue."""

    provider: IssueTrackerProviderStatusProvider
    url: str
    id: str
    state: IssueTrackerProviderStatusState
    team_id: UUID | Unset = UNSET
    state_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        provider: str = self.provider

        url = self.url

        id = self.id

        state: str = self.state

        team_id: str | Unset = UNSET
        if not isinstance(self.team_id, Unset):
            team_id = str(self.team_id)

        state_id: str | Unset = UNSET
        if not isinstance(self.state_id, Unset):
            state_id = str(self.state_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "provider": provider,
                "url": url,
                "id": id,
                "state": state,
            }
        )
        if team_id is not UNSET:
            field_dict["team_id"] = team_id
        if state_id is not UNSET:
            field_dict["state_id"] = state_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        provider = check_issue_tracker_provider_status_provider(d.pop("provider"))

        url = d.pop("url")

        id = d.pop("id")

        state = check_issue_tracker_provider_status_state(d.pop("state"))

        _team_id = d.pop("team_id", UNSET)
        team_id: UUID | Unset
        if isinstance(_team_id, Unset):
            team_id = UNSET
        else:
            team_id = UUID(_team_id)

        _state_id = d.pop("state_id", UNSET)
        state_id: UUID | Unset
        if isinstance(_state_id, Unset):
            state_id = UNSET
        else:
            state_id = UUID(_state_id)

        issue_tracker_provider_status = cls(
            provider=provider,
            url=url,
            id=id,
            state=state,
            team_id=team_id,
            state_id=state_id,
        )

        issue_tracker_provider_status.additional_properties = d
        return issue_tracker_provider_status

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
