from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_preparation_item_state import (
    ObsRuntimePreparationItemState,
    check_obs_runtime_preparation_item_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_handoff_collection import ObsRuntimeHandoffCollection
    from ..models.obs_runtime_preparation_observation import ObsRuntimePreparationObservation
    from ..models.obs_runtime_preparation_phase import ObsRuntimePreparationPhase


T = TypeVar("T", bound="ObsRuntimePreparationItem")


@_attrs_define
class ObsRuntimePreparationItem:
    """One sanitized image worker recovery scope. Collection status, observation expectation and three ordered phase
    observations are validated independently; no build, app, or deployment identity is exposed.

    """

    state: ObsRuntimePreparationItemState
    collection: ObsRuntimeHandoffCollection
    """Freshness and shape of the collection-status gauges. Available metadata can report unsuccessful or expired
    collection; that never establishes an empty backlog."""
    observation: ObsRuntimePreparationObservation
    """Whether the preparation observer is expected, independently of collection success and pending work. A false
    enabled bit can coexist with pending checkpoints."""
    phases: list[ObsRuntimePreparationPhase]
    node: str | Unset = UNSET
    target_id: str | Unset = UNSET
    unexpected: bool | Unset = UNSET
    scrape_observed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        collection = self.collection.to_dict()

        observation = self.observation.to_dict()

        phases = []
        for phases_item_data in self.phases:
            phases_item = phases_item_data.to_dict()
            phases.append(phases_item)

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
                "state": state,
                "collection": collection,
                "observation": observation,
                "phases": phases,
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
        from ..models.obs_runtime_handoff_collection import ObsRuntimeHandoffCollection
        from ..models.obs_runtime_preparation_observation import ObsRuntimePreparationObservation
        from ..models.obs_runtime_preparation_phase import ObsRuntimePreparationPhase

        d = dict(src_dict)
        state = check_obs_runtime_preparation_item_state(d.pop("state"))

        collection = ObsRuntimeHandoffCollection.from_dict(d.pop("collection"))

        observation = ObsRuntimePreparationObservation.from_dict(d.pop("observation"))

        phases = []
        _phases = d.pop("phases")
        for phases_item_data in _phases:
            phases_item = ObsRuntimePreparationPhase.from_dict(phases_item_data)

            phases.append(phases_item)

        node = d.pop("node", UNSET)

        target_id = d.pop("target_id", UNSET)

        unexpected = d.pop("unexpected", UNSET)

        _scrape_observed_at = d.pop("scrape_observed_at", UNSET)
        scrape_observed_at: datetime.datetime | Unset
        if isinstance(_scrape_observed_at, Unset):
            scrape_observed_at = UNSET
        else:
            scrape_observed_at = datetime.datetime.fromisoformat(_scrape_observed_at)

        obs_runtime_preparation_item = cls(
            state=state,
            collection=collection,
            observation=observation,
            phases=phases,
            node=node,
            target_id=target_id,
            unexpected=unexpected,
            scrape_observed_at=scrape_observed_at,
        )

        obs_runtime_preparation_item.additional_properties = d
        return obs_runtime_preparation_item

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
