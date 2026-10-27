from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_health_signal_reason import ObsHealthSignalReason, check_obs_health_signal_reason
from ..models.obs_health_signal_state import ObsHealthSignalState, check_obs_health_signal_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsHealthSignal")


@_attrs_define
class ObsHealthSignal:
    """Evidence for one Prometheus-derived health value. Freshness uses the
    oldest last-observed counter sample or faas.rules.yml rule-group
    evaluation across series, never the aggregate query's output timestamp.
    Historical observations are searched with a bounded 15-minute range.
    Removed series/groups can conservatively hold a signal stale until
    that history expires. A healthy state describes fresh query evidence,
    not whether the metric's value indicates an operational problem.

    """

    state: ObsHealthSignalState
    """Healthy means a valid value and an observation no older than
    max_age_seconds. No_samples means a missing observation or too
    few range samples. Stale means a known observation is too old.
    Unavailable means missing configuration, a failed query, or invalid
    upstream values. No last-good values are cached across requests.
    """
    window_seconds: int
    """Aggregation window; 300 for audit counters, 0 for the instantaneous alert count."""
    max_age_seconds: int
    """Freshness threshold; currently 120 seconds."""
    observed_at: datetime.datetime | Unset = UNSET
    """Underlying observation time (UTC), when known; retained for stale evidence."""
    reason: ObsHealthSignalReason | Unset = UNSET
    """Closed reason code for non-healthy signals; never raw upstream errors."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        window_seconds = self.window_seconds

        max_age_seconds = self.max_age_seconds

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "window_seconds": window_seconds,
                "max_age_seconds": max_age_seconds,
            }
        )
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_obs_health_signal_state(d.pop("state"))

        window_seconds = d.pop("window_seconds")

        max_age_seconds = d.pop("max_age_seconds")

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        _reason = d.pop("reason", UNSET)
        reason: ObsHealthSignalReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_health_signal_reason(_reason)

        obs_health_signal = cls(
            state=state,
            window_seconds=window_seconds,
            max_age_seconds=max_age_seconds,
            observed_at=observed_at,
            reason=reason,
        )

        obs_health_signal.additional_properties = d
        return obs_health_signal

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
