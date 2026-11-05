from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_notification_signal_state import ObsNotificationSignalState, check_obs_notification_signal_state
from ..models.obs_notification_signal_window_seconds import (
    ObsNotificationSignalWindowSeconds,
    check_obs_notification_signal_window_seconds,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsNotificationSignal")


@_attrs_define
class ObsNotificationSignal:
    """A usable value requires healthy evidence; null distinguishes missing or stale evidence from a fresh zero. Observed
    time comes from the oldest underlying last-observed sample across matching series.

    """

    state: ObsNotificationSignalState
    value: float | None
    window_seconds: ObsNotificationSignalWindowSeconds
    max_age_seconds: Literal[120]
    reason: str | Unset = UNSET
    observed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        value: float | None
        value = self.value

        window_seconds: int = self.window_seconds

        max_age_seconds = self.max_age_seconds

        reason = self.reason

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "value": value,
                "window_seconds": window_seconds,
                "max_age_seconds": max_age_seconds,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_obs_notification_signal_state(d.pop("state"))

        def _parse_value(data: object) -> float | None:
            if data is None:
                return data
            return cast(float | None, data)

        value = _parse_value(d.pop("value"))

        window_seconds = check_obs_notification_signal_window_seconds(d.pop("window_seconds"))

        max_age_seconds = cast(Literal[120], d.pop("max_age_seconds"))
        if max_age_seconds != 120:
            raise ValueError(f"max_age_seconds must match const 120, got '{max_age_seconds}'")

        reason = d.pop("reason", UNSET)

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        obs_notification_signal = cls(
            state=state,
            value=value,
            window_seconds=window_seconds,
            max_age_seconds=max_age_seconds,
            reason=reason,
            observed_at=observed_at,
        )

        obs_notification_signal.additional_properties = d
        return obs_notification_signal

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
