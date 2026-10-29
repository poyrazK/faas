from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.obs_alert_recurrence_duration import ObsAlertRecurrenceDuration
    from ..models.obs_alert_recurrence_gap import ObsAlertRecurrenceGap


T = TypeVar("T", bound="ObsAlertRecurrenceMetrics")


@_attrs_define
class ObsAlertRecurrenceMetrics:
    """Episode denominator and adjacent-repeat denominator with independent evidence partitions."""

    episodes: int
    repeat_activations: int
    unresolved: int
    unknown_observation: int
    duration: ObsAlertRecurrenceDuration
    """Episode partition and valid activation-to-resolution observation statistics."""
    recovery_gap: ObsAlertRecurrenceGap
    """Adjacent-repeat partition; valid samples measure confirmed resolution observation to next activation."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        episodes = self.episodes

        repeat_activations = self.repeat_activations

        unresolved = self.unresolved

        unknown_observation = self.unknown_observation

        duration = self.duration.to_dict()

        recovery_gap = self.recovery_gap.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "episodes": episodes,
                "repeat_activations": repeat_activations,
                "unresolved": unresolved,
                "unknown_observation": unknown_observation,
                "duration": duration,
                "recovery_gap": recovery_gap,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_alert_recurrence_duration import ObsAlertRecurrenceDuration
        from ..models.obs_alert_recurrence_gap import ObsAlertRecurrenceGap

        d = dict(src_dict)
        episodes = d.pop("episodes")

        repeat_activations = d.pop("repeat_activations")

        unresolved = d.pop("unresolved")

        unknown_observation = d.pop("unknown_observation")

        duration = ObsAlertRecurrenceDuration.from_dict(d.pop("duration"))

        recovery_gap = ObsAlertRecurrenceGap.from_dict(d.pop("recovery_gap"))

        obs_alert_recurrence_metrics = cls(
            episodes=episodes,
            repeat_activations=repeat_activations,
            unresolved=unresolved,
            unknown_observation=unknown_observation,
            duration=duration,
            recovery_gap=recovery_gap,
        )

        obs_alert_recurrence_metrics.additional_properties = d
        return obs_alert_recurrence_metrics

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
