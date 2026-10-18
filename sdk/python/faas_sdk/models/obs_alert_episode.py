from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_episode_observation_state import (
    ObsAlertEpisodeObservationState,
    check_obs_alert_episode_observation_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsAlertEpisode")


@_attrs_define
class ObsAlertEpisode:
    """Immutable recurrence identity and retained observation clocks on a private operator incident."""

    series_id: str
    started_at: datetime.datetime
    first_seen_at: datetime.datetime
    last_seen_at: datetime.datetime
    last_evaluated_at: datetime.datetime
    observation_state: ObsAlertEpisodeObservationState
    resolved_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        series_id = self.series_id

        started_at = self.started_at.isoformat()

        first_seen_at = self.first_seen_at.isoformat()

        last_seen_at = self.last_seen_at.isoformat()

        last_evaluated_at = self.last_evaluated_at.isoformat()

        observation_state: str = self.observation_state

        resolved_at: str | Unset = UNSET
        if not isinstance(self.resolved_at, Unset):
            resolved_at = self.resolved_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "series_id": series_id,
                "started_at": started_at,
                "first_seen_at": first_seen_at,
                "last_seen_at": last_seen_at,
                "last_evaluated_at": last_evaluated_at,
                "observation_state": observation_state,
            }
        )
        if resolved_at is not UNSET:
            field_dict["resolved_at"] = resolved_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        series_id = d.pop("series_id")

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        first_seen_at = datetime.datetime.fromisoformat(d.pop("first_seen_at"))

        last_seen_at = datetime.datetime.fromisoformat(d.pop("last_seen_at"))

        last_evaluated_at = datetime.datetime.fromisoformat(d.pop("last_evaluated_at"))

        observation_state = check_obs_alert_episode_observation_state(d.pop("observation_state"))

        _resolved_at = d.pop("resolved_at", UNSET)
        resolved_at: datetime.datetime | Unset
        if isinstance(_resolved_at, Unset):
            resolved_at = UNSET
        else:
            resolved_at = datetime.datetime.fromisoformat(_resolved_at)

        obs_alert_episode = cls(
            series_id=series_id,
            started_at=started_at,
            first_seen_at=first_seen_at,
            last_seen_at=last_seen_at,
            last_evaluated_at=last_evaluated_at,
            observation_state=observation_state,
            resolved_at=resolved_at,
        )

        obs_alert_episode.additional_properties = d
        return obs_alert_episode

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
