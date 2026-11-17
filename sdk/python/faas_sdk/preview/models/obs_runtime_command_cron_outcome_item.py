from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_command_cron_outcome_item_job import (
    ObsRuntimeCommandCronOutcomeItemJob,
    check_obs_runtime_command_cron_outcome_item_job,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_command_cron_outcome_signal import ObsRuntimeCommandCronOutcomeSignal
    from ..models.obs_runtime_process_item import ObsRuntimeProcessItem


T = TypeVar("T", bound="ObsRuntimeCommandCronOutcomeItem")


@_attrs_define
class ObsRuntimeCommandCronOutcomeItem:
    """Sanitized scheduler process proof and 29 ordered source/outcome signals. The comparison clock matches the core
    process section. Preview details yield first under the shared response limit, preserving aggregate coverage and
    explicit omissions. No cron, command, occurrence, task or app identity, raw target address, output, token or
    provider error is exposed.

    """

    job: ObsRuntimeCommandCronOutcomeItemJob
    process: ObsRuntimeProcessItem
    """Original uptime and sample clocks; comparison uses the same complete target identity and permits clock skew.
    Optional fields are absent for unavailable evidence."""
    signals: list[ObsRuntimeCommandCronOutcomeSignal]
    node: str | Unset = UNSET
    target_id: str | Unset = UNSET
    unexpected: bool | Unset = UNSET
    scrape_observed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        job: str = self.job

        process = self.process.to_dict()

        signals = []
        for signals_item_data in self.signals:
            signals_item = signals_item_data.to_dict()
            signals.append(signals_item)

        node = self.node

        target_id = self.target_id

        unexpected = self.unexpected

        scrape_observed_at: str | Unset = UNSET
        if not isinstance(self.scrape_observed_at, Unset):
            scrape_observed_at = self.scrape_observed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "job": job,
                "process": process,
                "signals": signals,
            }
        )
        if node is not UNSET:
            field_dict["node"] = node
        if target_id is not UNSET:
            field_dict["target_id"] = target_id
        if unexpected is not UNSET:
            field_dict["unexpected"] = unexpected
        if scrape_observed_at is not UNSET:
            field_dict["scrape_observed_at"] = scrape_observed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_runtime_command_cron_outcome_signal import ObsRuntimeCommandCronOutcomeSignal
        from ..models.obs_runtime_process_item import ObsRuntimeProcessItem

        d = dict(src_dict)
        job = check_obs_runtime_command_cron_outcome_item_job(d.pop("job"))

        process = ObsRuntimeProcessItem.from_dict(d.pop("process"))

        signals = []
        _signals = d.pop("signals")
        for signals_item_data in _signals:
            signals_item = ObsRuntimeCommandCronOutcomeSignal.from_dict(signals_item_data)

            signals.append(signals_item)

        node = d.pop("node", UNSET)

        target_id = d.pop("target_id", UNSET)

        unexpected = d.pop("unexpected", UNSET)

        _scrape_observed_at = d.pop("scrape_observed_at", UNSET)
        scrape_observed_at: datetime.datetime | Unset
        if isinstance(_scrape_observed_at, Unset):
            scrape_observed_at = UNSET
        else:
            scrape_observed_at = datetime.datetime.fromisoformat(_scrape_observed_at)

        obs_runtime_command_cron_outcome_item = cls(
            job=job,
            process=process,
            signals=signals,
            node=node,
            target_id=target_id,
            unexpected=unexpected,
            scrape_observed_at=scrape_observed_at,
        )

        obs_runtime_command_cron_outcome_item.additional_properties = d
        return obs_runtime_command_cron_outcome_item

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
