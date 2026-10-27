from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RealtimeConsumerGroupLagAlert")


@_attrs_define
class RealtimeConsumerGroupLagAlert:
    """Realtime Consumer Group Lag Alert."""

    enabled: bool
    breached: bool
    unassigned: None | str
    oldest_pending_age_seconds: int | None
    dead_letters: int | None
    observed_at: datetime.datetime | None
    last_event_at: datetime.datetime | None
    """Last event decision, including when no recipient opted in."""
    unassigned_threshold: str = "0"
    """this realtime operation. 0 disables backlog threshold; maximum 1000000000."""
    oldest_pending_seconds: int = 0
    dead_letter_threshold: int = 0
    cooldown_seconds: int = 300
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        unassigned_threshold = self.unassigned_threshold

        oldest_pending_seconds = self.oldest_pending_seconds

        dead_letter_threshold = self.dead_letter_threshold

        cooldown_seconds = self.cooldown_seconds

        breached = self.breached

        unassigned: None | str
        unassigned = self.unassigned

        oldest_pending_age_seconds: int | None
        oldest_pending_age_seconds = self.oldest_pending_age_seconds

        dead_letters: int | None
        dead_letters = self.dead_letters

        observed_at: None | str
        if isinstance(self.observed_at, datetime.datetime):
            observed_at = self.observed_at.isoformat()
        else:
            observed_at = self.observed_at

        last_event_at: None | str
        if isinstance(self.last_event_at, datetime.datetime):
            last_event_at = self.last_event_at.isoformat()
        else:
            last_event_at = self.last_event_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "unassigned_threshold": unassigned_threshold,
                "oldest_pending_seconds": oldest_pending_seconds,
                "dead_letter_threshold": dead_letter_threshold,
                "cooldown_seconds": cooldown_seconds,
                "breached": breached,
                "unassigned": unassigned,
                "oldest_pending_age_seconds": oldest_pending_age_seconds,
                "dead_letters": dead_letters,
                "observed_at": observed_at,
                "last_event_at": last_event_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        unassigned_threshold = d.pop("unassigned_threshold")

        oldest_pending_seconds = d.pop("oldest_pending_seconds")

        dead_letter_threshold = d.pop("dead_letter_threshold")

        cooldown_seconds = d.pop("cooldown_seconds")

        breached = d.pop("breached")

        def _parse_unassigned(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        unassigned = _parse_unassigned(d.pop("unassigned"))

        def _parse_oldest_pending_age_seconds(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        oldest_pending_age_seconds = _parse_oldest_pending_age_seconds(d.pop("oldest_pending_age_seconds"))

        def _parse_dead_letters(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        dead_letters = _parse_dead_letters(d.pop("dead_letters"))

        def _parse_observed_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                observed_at_type_0 = datetime.datetime.fromisoformat(data)

                return observed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        observed_at = _parse_observed_at(d.pop("observed_at"))

        def _parse_last_event_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_event_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_event_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        last_event_at = _parse_last_event_at(d.pop("last_event_at"))

        realtime_consumer_group_lag_alert = cls(
            enabled=enabled,
            unassigned_threshold=unassigned_threshold,
            oldest_pending_seconds=oldest_pending_seconds,
            dead_letter_threshold=dead_letter_threshold,
            cooldown_seconds=cooldown_seconds,
            breached=breached,
            unassigned=unassigned,
            oldest_pending_age_seconds=oldest_pending_age_seconds,
            dead_letters=dead_letters,
            observed_at=observed_at,
            last_event_at=last_event_at,
        )

        realtime_consumer_group_lag_alert.additional_properties = d
        return realtime_consumer_group_lag_alert

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
