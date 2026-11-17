from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_build_recovery_item_job import (
    ObsRuntimeBuildRecoveryItemJob,
    check_obs_runtime_build_recovery_item_job,
)
from ..models.obs_runtime_build_recovery_item_state import (
    ObsRuntimeBuildRecoveryItemState,
    check_obs_runtime_build_recovery_item_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_build_recovery_cleanup import ObsRuntimeBuildRecoveryCleanup
    from ..models.obs_runtime_build_recovery_running import ObsRuntimeBuildRecoveryRunning
    from ..models.obs_runtime_handoff_collection import ObsRuntimeHandoffCollection


T = TypeVar("T", bound="ObsRuntimeBuildRecoveryItem")


@_attrs_define
class ObsRuntimeBuildRecoveryItem:
    """One sanitized API replica view. Collection status, running claims and cleanup obligations are independently checked
    against source and original clocks. Successful fresh collection is a prerequisite for both work components; valid
    partial positives survive another component gap.

    """

    job: ObsRuntimeBuildRecoveryItemJob
    state: ObsRuntimeBuildRecoveryItemState
    collection: ObsRuntimeHandoffCollection
    """Freshness and shape of the collection-status gauges. Available metadata can report unsuccessful or expired
    collection; that never establishes an empty backlog."""
    running: ObsRuntimeBuildRecoveryRunning
    """Running build-claim evidence requires fresh successful collection after the evidence boundary and coherent
    original scrape clocks. Missing or failed collection never becomes an empty set."""
    cleanup: ObsRuntimeBuildRecoveryCleanup
    """Durable outstanding VM cleanup obligations, including live claims and retries. Usable values require
    successful fresh collection and coherent source clocks. Nonempty evidence is advisory and does not prove a VM is
    still alive."""
    node: str | Unset = UNSET
    target_id: str | Unset = UNSET
    unexpected: bool | Unset = UNSET
    scrape_observed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        job: str = self.job

        state: str = self.state

        collection = self.collection.to_dict()

        running = self.running.to_dict()

        cleanup = self.cleanup.to_dict()

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
                "running": running,
                "cleanup": cleanup,
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
        from ..models.obs_runtime_build_recovery_cleanup import ObsRuntimeBuildRecoveryCleanup
        from ..models.obs_runtime_build_recovery_running import ObsRuntimeBuildRecoveryRunning
        from ..models.obs_runtime_handoff_collection import ObsRuntimeHandoffCollection

        d = dict(src_dict)
        job = check_obs_runtime_build_recovery_item_job(d.pop("job"))

        state = check_obs_runtime_build_recovery_item_state(d.pop("state"))

        collection = ObsRuntimeHandoffCollection.from_dict(d.pop("collection"))

        running = ObsRuntimeBuildRecoveryRunning.from_dict(d.pop("running"))

        cleanup = ObsRuntimeBuildRecoveryCleanup.from_dict(d.pop("cleanup"))

        node = d.pop("node", UNSET)

        target_id = d.pop("target_id", UNSET)

        unexpected = d.pop("unexpected", UNSET)

        _scrape_observed_at = d.pop("scrape_observed_at", UNSET)
        scrape_observed_at: datetime.datetime | Unset
        if isinstance(_scrape_observed_at, Unset):
            scrape_observed_at = UNSET
        else:
            scrape_observed_at = datetime.datetime.fromisoformat(_scrape_observed_at)

        obs_runtime_build_recovery_item = cls(
            job=job,
            state=state,
            collection=collection,
            running=running,
            cleanup=cleanup,
            node=node,
            target_id=target_id,
            unexpected=unexpected,
            scrape_observed_at=scrape_observed_at,
        )

        obs_runtime_build_recovery_item.additional_properties = d
        return obs_runtime_build_recovery_item

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
