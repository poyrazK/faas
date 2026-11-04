from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_recurrence_episode_status import (
    ObsAlertRecurrenceEpisodeStatus,
    check_obs_alert_recurrence_episode_status,
)

T = TypeVar("T", bound="ObsAlertRecurrenceEpisode")


@_attrs_define
class ObsAlertRecurrenceEpisode:
    """Immutable retained episode reference for recurrence investigation without raw source payloads or workflow
    identities.

    """

    episode_id: str
    status: ObsAlertRecurrenceEpisodeStatus
    started_at: datetime.datetime
    resolved_at: datetime.datetime | None
    evidence_path: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        episode_id = self.episode_id

        status: str = self.status

        started_at = self.started_at.isoformat()

        resolved_at: None | str
        if isinstance(self.resolved_at, datetime.datetime):
            resolved_at = self.resolved_at.isoformat()
        else:
            resolved_at = self.resolved_at

        evidence_path = self.evidence_path

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "episode_id": episode_id,
                "status": status,
                "started_at": started_at,
                "resolved_at": resolved_at,
                "evidence_path": evidence_path,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        episode_id = d.pop("episode_id")

        status = check_obs_alert_recurrence_episode_status(d.pop("status"))

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        def _parse_resolved_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                resolved_at_type_0 = datetime.datetime.fromisoformat(data)

                return resolved_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        resolved_at = _parse_resolved_at(d.pop("resolved_at"))

        evidence_path = d.pop("evidence_path")

        obs_alert_recurrence_episode = cls(
            episode_id=episode_id,
            status=status,
            started_at=started_at,
            resolved_at=resolved_at,
            evidence_path=evidence_path,
        )

        obs_alert_recurrence_episode.additional_properties = d
        return obs_alert_recurrence_episode

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
