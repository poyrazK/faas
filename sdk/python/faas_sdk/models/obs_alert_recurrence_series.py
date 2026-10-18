from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_recurrence_series_severity import (
    ObsAlertRecurrenceSeriesSeverity,
    check_obs_alert_recurrence_series_severity,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_alert_recurrence_episode import ObsAlertRecurrenceEpisode
    from ..models.obs_alert_recurrence_metrics import ObsAlertRecurrenceMetrics


T = TypeVar("T", bound="ObsAlertRecurrenceSeries")


@_attrs_define
class ObsAlertRecurrenceSeries:
    """Exact rule/series recurrence group with safe resource identity and a bounded newest-first episode preview."""

    rule_key: str
    series_id: str
    name: str
    severity: ObsAlertRecurrenceSeriesSeverity
    component: str
    first_activation_at: datetime.datetime
    last_activation_at: datetime.datetime
    metrics: ObsAlertRecurrenceMetrics
    """Episode denominator and adjacent-repeat denominator with independent evidence partitions."""
    recent_episodes: list[ObsAlertRecurrenceEpisode]
    recent_episodes_truncated: bool
    """Whether older cohort episodes of this series were omitted from the preview."""
    account_id: UUID | Unset = UNSET
    app_id: UUID | Unset = UNSET
    node_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rule_key = self.rule_key

        series_id = self.series_id

        name = self.name

        severity: str = self.severity

        component = self.component

        first_activation_at = self.first_activation_at.isoformat()

        last_activation_at = self.last_activation_at.isoformat()

        metrics = self.metrics.to_dict()

        recent_episodes = []
        for recent_episodes_item_data in self.recent_episodes:
            recent_episodes_item = recent_episodes_item_data.to_dict()
            recent_episodes.append(recent_episodes_item)

        recent_episodes_truncated = self.recent_episodes_truncated

        account_id: str | Unset = UNSET
        if not isinstance(self.account_id, Unset):
            account_id = str(self.account_id)

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        node_id: str | Unset = UNSET
        if not isinstance(self.node_id, Unset):
            node_id = str(self.node_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rule_key": rule_key,
                "series_id": series_id,
                "name": name,
                "severity": severity,
                "component": component,
                "first_activation_at": first_activation_at,
                "last_activation_at": last_activation_at,
                "metrics": metrics,
                "recent_episodes": recent_episodes,
                "recent_episodes_truncated": recent_episodes_truncated,
            }
        )
        if account_id is not UNSET:
            field_dict["account_id"] = account_id
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if node_id is not UNSET:
            field_dict["node_id"] = node_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_alert_recurrence_episode import ObsAlertRecurrenceEpisode
        from ..models.obs_alert_recurrence_metrics import ObsAlertRecurrenceMetrics

        d = dict(src_dict)
        rule_key = d.pop("rule_key")

        series_id = d.pop("series_id")

        name = d.pop("name")

        severity = check_obs_alert_recurrence_series_severity(d.pop("severity"))

        component = d.pop("component")

        first_activation_at = datetime.datetime.fromisoformat(d.pop("first_activation_at"))

        last_activation_at = datetime.datetime.fromisoformat(d.pop("last_activation_at"))

        metrics = ObsAlertRecurrenceMetrics.from_dict(d.pop("metrics"))

        recent_episodes = []
        _recent_episodes = d.pop("recent_episodes")
        for recent_episodes_item_data in _recent_episodes:
            recent_episodes_item = ObsAlertRecurrenceEpisode.from_dict(recent_episodes_item_data)

            recent_episodes.append(recent_episodes_item)

        recent_episodes_truncated = d.pop("recent_episodes_truncated")

        _account_id = d.pop("account_id", UNSET)
        account_id: UUID | Unset
        if isinstance(_account_id, Unset):
            account_id = UNSET
        else:
            account_id = UUID(_account_id)

        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        _node_id = d.pop("node_id", UNSET)
        node_id: UUID | Unset
        if isinstance(_node_id, Unset):
            node_id = UNSET
        else:
            node_id = UUID(_node_id)

        obs_alert_recurrence_series = cls(
            rule_key=rule_key,
            series_id=series_id,
            name=name,
            severity=severity,
            component=component,
            first_activation_at=first_activation_at,
            last_activation_at=last_activation_at,
            metrics=metrics,
            recent_episodes=recent_episodes,
            recent_episodes_truncated=recent_episodes_truncated,
            account_id=account_id,
            app_id=app_id,
            node_id=node_id,
        )

        obs_alert_recurrence_series.additional_properties = d
        return obs_alert_recurrence_series

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
