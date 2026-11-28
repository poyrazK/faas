from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_input_job_job import ObsInputJobJob, check_obs_input_job_job
from ..models.obs_input_job_state import ObsInputJobState, check_obs_input_job_state

if TYPE_CHECKING:
    from ..models.obs_input_expectation import ObsInputExpectation
    from ..models.obs_input_metric import ObsInputMetric
    from ..models.obs_input_targets import ObsInputTargets


T = TypeVar("T", bound="ObsInputJob")


@_attrs_define
class ObsInputJob:
    """One selected job's independent expectation, scrape and gauge evidence."""

    job: ObsInputJobJob
    state: ObsInputJobState
    expectation: ObsInputExpectation
    """Explicit static minimum or validated fresh compute discovery count; unknown counts remain null."""
    targets: ObsInputTargets
    """Configured scrape coverage with known failures retained independently of unknown expectations."""
    metrics: list[ObsInputMetric]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        job: str = self.job

        state: str = self.state

        expectation = self.expectation.to_dict()

        targets = self.targets.to_dict()

        metrics = []
        for metrics_item_data in self.metrics:
            metrics_item = metrics_item_data.to_dict()
            metrics.append(metrics_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "job": job,
                "state": state,
                "expectation": expectation,
                "targets": targets,
                "metrics": metrics,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_input_expectation import ObsInputExpectation
        from ..models.obs_input_metric import ObsInputMetric
        from ..models.obs_input_targets import ObsInputTargets

        d = dict(src_dict)
        job = check_obs_input_job_job(d.pop("job"))

        state = check_obs_input_job_state(d.pop("state"))

        expectation = ObsInputExpectation.from_dict(d.pop("expectation"))

        targets = ObsInputTargets.from_dict(d.pop("targets"))

        metrics = []
        _metrics = d.pop("metrics")
        for metrics_item_data in _metrics:
            metrics_item = ObsInputMetric.from_dict(metrics_item_data)

            metrics.append(metrics_item)

        obs_input_job = cls(
            job=job,
            state=state,
            expectation=expectation,
            targets=targets,
            metrics=metrics,
        )

        obs_input_job.additional_properties = d
        return obs_input_job

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
