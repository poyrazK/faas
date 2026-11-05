from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_collection_coverage_scope import (
    ObsAlertCollectionCoverageScope,
    check_obs_alert_collection_coverage_scope,
)
from ..models.obs_alert_collection_coverage_state import (
    ObsAlertCollectionCoverageState,
    check_obs_alert_collection_coverage_state,
)

T = TypeVar("T", bound="ObsAlertCollectionCoverage")


@_attrs_define
class ObsAlertCollectionCoverage:
    """Exact prospective cadence-evidence partition of a requested retained window; no continuous-availability claim."""

    since: datetime.datetime
    as_of: datetime.datetime
    scope: ObsAlertCollectionCoverageScope
    state: ObsAlertCollectionCoverageState
    """Observed means every interval has recent successful outcome evidence; partial includes failed or missing
    intervals; unknown has no fresh duration evidence."""
    max_age_seconds: Literal[60]
    retention_seconds: Literal[2592000]
    window_microseconds: int
    successful_observation_microseconds: int
    failed_observation_microseconds: int
    unknown_microseconds: int
    successes: int
    failures: int
    limitations: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        since = self.since.isoformat()

        as_of = self.as_of.isoformat()

        scope: str = self.scope

        state: str = self.state

        max_age_seconds = self.max_age_seconds

        retention_seconds = self.retention_seconds

        window_microseconds = self.window_microseconds

        successful_observation_microseconds = self.successful_observation_microseconds

        failed_observation_microseconds = self.failed_observation_microseconds

        unknown_microseconds = self.unknown_microseconds

        successes = self.successes

        failures = self.failures

        limitations = self.limitations

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "since": since,
                "as_of": as_of,
                "scope": scope,
                "state": state,
                "max_age_seconds": max_age_seconds,
                "retention_seconds": retention_seconds,
                "window_microseconds": window_microseconds,
                "successful_observation_microseconds": successful_observation_microseconds,
                "failed_observation_microseconds": failed_observation_microseconds,
                "unknown_microseconds": unknown_microseconds,
                "successes": successes,
                "failures": failures,
                "limitations": limitations,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        since = datetime.datetime.fromisoformat(d.pop("since"))

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        scope = check_obs_alert_collection_coverage_scope(d.pop("scope"))

        state = check_obs_alert_collection_coverage_state(d.pop("state"))

        max_age_seconds = cast(Literal[60], d.pop("max_age_seconds"))
        if max_age_seconds != 60:
            raise ValueError(f"max_age_seconds must match const 60, got '{max_age_seconds}'")

        retention_seconds = cast(Literal[2592000], d.pop("retention_seconds"))
        if retention_seconds != 2592000:
            raise ValueError(f"retention_seconds must match const 2592000, got '{retention_seconds}'")

        window_microseconds = d.pop("window_microseconds")

        successful_observation_microseconds = d.pop("successful_observation_microseconds")

        failed_observation_microseconds = d.pop("failed_observation_microseconds")

        unknown_microseconds = d.pop("unknown_microseconds")

        successes = d.pop("successes")

        failures = d.pop("failures")

        limitations = cast(list[str], d.pop("limitations"))

        obs_alert_collection_coverage = cls(
            since=since,
            as_of=as_of,
            scope=scope,
            state=state,
            max_age_seconds=max_age_seconds,
            retention_seconds=retention_seconds,
            window_microseconds=window_microseconds,
            successful_observation_microseconds=successful_observation_microseconds,
            failed_observation_microseconds=failed_observation_microseconds,
            unknown_microseconds=unknown_microseconds,
            successes=successes,
            failures=failures,
            limitations=limitations,
        )

        obs_alert_collection_coverage.additional_properties = d
        return obs_alert_collection_coverage

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
