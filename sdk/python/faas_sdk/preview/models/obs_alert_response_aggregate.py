from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.obs_alert_response_coverage import ObsAlertResponseCoverage
    from ..models.obs_alert_response_metric import ObsAlertResponseMetric


T = TypeVar("T", bound="ObsAlertResponseAggregate")


@_attrs_define
class ObsAlertResponseAggregate:
    """One cohort denominator shared by independent acknowledgment and observed recovery classifications."""

    episodes: int
    unresolved: int
    unknown_observation: int
    acknowledgment: ObsAlertResponseMetric
    """Complete cohort partition and valid-sample percentiles without substituting zeros for missing evidence."""
    resolution: ObsAlertResponseMetric
    """Complete cohort partition and valid-sample percentiles without substituting zeros for missing evidence."""
    receipt_coverage: ObsAlertResponseCoverage
    """Disjoint receipt-evidence classifications against each episode's saved workflow revision."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        episodes = self.episodes

        unresolved = self.unresolved

        unknown_observation = self.unknown_observation

        acknowledgment = self.acknowledgment.to_dict()

        resolution = self.resolution.to_dict()

        receipt_coverage = self.receipt_coverage.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "episodes": episodes,
                "unresolved": unresolved,
                "unknown_observation": unknown_observation,
                "acknowledgment": acknowledgment,
                "resolution": resolution,
                "receipt_coverage": receipt_coverage,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_alert_response_coverage import ObsAlertResponseCoverage
        from ..models.obs_alert_response_metric import ObsAlertResponseMetric

        d = dict(src_dict)
        episodes = d.pop("episodes")

        unresolved = d.pop("unresolved")

        unknown_observation = d.pop("unknown_observation")

        acknowledgment = ObsAlertResponseMetric.from_dict(d.pop("acknowledgment"))

        resolution = ObsAlertResponseMetric.from_dict(d.pop("resolution"))

        receipt_coverage = ObsAlertResponseCoverage.from_dict(d.pop("receipt_coverage"))

        obs_alert_response_aggregate = cls(
            episodes=episodes,
            unresolved=unresolved,
            unknown_observation=unknown_observation,
            acknowledgment=acknowledgment,
            resolution=resolution,
            receipt_coverage=receipt_coverage,
        )

        obs_alert_response_aggregate.additional_properties = d
        return obs_alert_response_aggregate

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
