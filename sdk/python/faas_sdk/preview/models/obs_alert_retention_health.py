from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_retention_health_state import (
    ObsAlertRetentionHealthState,
    check_obs_alert_retention_health_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsAlertRetentionHealth")


@_attrs_define
class ObsAlertRetentionHealth:
    """Independent retention receipt and exact expired-episode backlog at the snapshot cutoff."""

    state: ObsAlertRetentionHealthState
    as_of: datetime.datetime
    cutoff: datetime.datetime
    last_pruned_episodes: int | None
    expired_episodes: int
    reason: str | Unset = UNSET
    last_attempt_at: datetime.datetime | Unset = UNSET
    last_success_at: datetime.datetime | Unset = UNSET
    oldest_expired_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        as_of = self.as_of.isoformat()

        cutoff = self.cutoff.isoformat()

        last_pruned_episodes: int | None
        last_pruned_episodes = self.last_pruned_episodes

        expired_episodes = self.expired_episodes

        reason = self.reason

        last_attempt_at: str | Unset = UNSET
        if not isinstance(self.last_attempt_at, Unset):
            last_attempt_at = self.last_attempt_at.isoformat()

        last_success_at: str | Unset = UNSET
        if not isinstance(self.last_success_at, Unset):
            last_success_at = self.last_success_at.isoformat()

        oldest_expired_at: str | Unset = UNSET
        if not isinstance(self.oldest_expired_at, Unset):
            oldest_expired_at = self.oldest_expired_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "as_of": as_of,
                "cutoff": cutoff,
                "last_pruned_episodes": last_pruned_episodes,
                "expired_episodes": expired_episodes,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if last_attempt_at is not UNSET:
            field_dict["last_attempt_at"] = last_attempt_at
        if last_success_at is not UNSET:
            field_dict["last_success_at"] = last_success_at
        if oldest_expired_at is not UNSET:
            field_dict["oldest_expired_at"] = oldest_expired_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_obs_alert_retention_health_state(d.pop("state"))

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        cutoff = datetime.datetime.fromisoformat(d.pop("cutoff"))

        def _parse_last_pruned_episodes(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        last_pruned_episodes = _parse_last_pruned_episodes(d.pop("last_pruned_episodes"))

        expired_episodes = d.pop("expired_episodes")

        reason = d.pop("reason", UNSET)

        _last_attempt_at = d.pop("last_attempt_at", UNSET)
        last_attempt_at: datetime.datetime | Unset
        if isinstance(_last_attempt_at, Unset):
            last_attempt_at = UNSET
        else:
            last_attempt_at = datetime.datetime.fromisoformat(_last_attempt_at)

        _last_success_at = d.pop("last_success_at", UNSET)
        last_success_at: datetime.datetime | Unset
        if isinstance(_last_success_at, Unset):
            last_success_at = UNSET
        else:
            last_success_at = datetime.datetime.fromisoformat(_last_success_at)

        _oldest_expired_at = d.pop("oldest_expired_at", UNSET)
        oldest_expired_at: datetime.datetime | Unset
        if isinstance(_oldest_expired_at, Unset):
            oldest_expired_at = UNSET
        else:
            oldest_expired_at = datetime.datetime.fromisoformat(_oldest_expired_at)

        obs_alert_retention_health = cls(
            state=state,
            as_of=as_of,
            cutoff=cutoff,
            last_pruned_episodes=last_pruned_episodes,
            expired_episodes=expired_episodes,
            reason=reason,
            last_attempt_at=last_attempt_at,
            last_success_at=last_success_at,
            oldest_expired_at=oldest_expired_at,
        )

        obs_alert_retention_health.additional_properties = d
        return obs_alert_retention_health

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
