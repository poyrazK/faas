from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_command_cron_item_job import (
    ObsRuntimeCommandCronItemJob,
    check_obs_runtime_command_cron_item_job,
)
from ..models.obs_runtime_command_cron_item_state import (
    ObsRuntimeCommandCronItemState,
    check_obs_runtime_command_cron_item_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_command_cron_class import ObsRuntimeCommandCronClass
    from ..models.obs_runtime_command_cron_observation import ObsRuntimeCommandCronObservation
    from ..models.obs_runtime_command_cron_sweep import ObsRuntimeCommandCronSweep
    from ..models.obs_runtime_handoff_collection import ObsRuntimeHandoffCollection


T = TypeVar("T", bound="ObsRuntimeCommandCronItem")


@_attrs_define
class ObsRuntimeCommandCronItem:
    """One sanitized scheduler ownership scope, identified by selected job and target. Collection, observer expectation,
    live scan activity and ten durable schedule classes remain distinct. No schedule, app, account, command or payload
    identity is exposed.

    """

    job: ObsRuntimeCommandCronItemJob
    state: ObsRuntimeCommandCronItemState
    collection: ObsRuntimeHandoffCollection
    """Freshness and shape of the collection-status gauges. Available metadata can report unsuccessful or expired
    collection; that never establishes an empty backlog."""
    observation: ObsRuntimeCommandCronObservation
    """Whether the loop expects independent schedule observation. False on shutdown does not imply empty schedules,
    disabled cron intent or completed work."""
    sweep: ObsRuntimeCommandCronSweep
    """Live shared cron-scan activity, independent of durable schedule collection. A successful scan confirms
    enumeration and owner lookup, not admission of every command. The scan can include inline HTTP work, so activity
    alone cannot identify a command-runner failure."""
    classes: list[ObsRuntimeCommandCronClass]
    node: str | Unset = UNSET
    target_id: str | Unset = UNSET
    unexpected: bool | Unset = UNSET
    scrape_observed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        job: str = self.job

        state: str = self.state

        collection = self.collection.to_dict()

        observation = self.observation.to_dict()

        sweep = self.sweep.to_dict()

        classes = []
        for classes_item_data in self.classes:
            classes_item = classes_item_data.to_dict()
            classes.append(classes_item)

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
                "state": state,
                "collection": collection,
                "observation": observation,
                "sweep": sweep,
                "classes": classes,
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
        from ..models.obs_runtime_command_cron_class import ObsRuntimeCommandCronClass
        from ..models.obs_runtime_command_cron_observation import ObsRuntimeCommandCronObservation
        from ..models.obs_runtime_command_cron_sweep import ObsRuntimeCommandCronSweep
        from ..models.obs_runtime_handoff_collection import ObsRuntimeHandoffCollection

        d = dict(src_dict)
        job = check_obs_runtime_command_cron_item_job(d.pop("job"))

        state = check_obs_runtime_command_cron_item_state(d.pop("state"))

        collection = ObsRuntimeHandoffCollection.from_dict(d.pop("collection"))

        observation = ObsRuntimeCommandCronObservation.from_dict(d.pop("observation"))

        sweep = ObsRuntimeCommandCronSweep.from_dict(d.pop("sweep"))

        classes = []
        _classes = d.pop("classes")
        for classes_item_data in _classes:
            classes_item = ObsRuntimeCommandCronClass.from_dict(classes_item_data)

            classes.append(classes_item)

        node = d.pop("node", UNSET)

        target_id = d.pop("target_id", UNSET)

        unexpected = d.pop("unexpected", UNSET)

        _scrape_observed_at = d.pop("scrape_observed_at", UNSET)
        scrape_observed_at: datetime.datetime | Unset
        if isinstance(_scrape_observed_at, Unset):
            scrape_observed_at = UNSET
        else:
            scrape_observed_at = datetime.datetime.fromisoformat(_scrape_observed_at)

        obs_runtime_command_cron_item = cls(
            job=job,
            state=state,
            collection=collection,
            observation=observation,
            sweep=sweep,
            classes=classes,
            node=node,
            target_id=target_id,
            unexpected=unexpected,
            scrape_observed_at=scrape_observed_at,
        )

        obs_runtime_command_cron_item.additional_properties = d
        return obs_runtime_command_cron_item

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
