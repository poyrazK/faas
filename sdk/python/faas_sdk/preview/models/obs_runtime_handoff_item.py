from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_handoff_item_state import ObsRuntimeHandoffItemState, check_obs_runtime_handoff_item_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_handoff_backlog import ObsRuntimeHandoffBacklog
    from ..models.obs_runtime_handoff_collection import ObsRuntimeHandoffCollection
    from ..models.obs_runtime_handoff_recovery import ObsRuntimeHandoffRecovery


T = TypeVar("T", bound="ObsRuntimeHandoffItem")


@_attrs_define
class ObsRuntimeHandoffItem:
    """One sanitized image worker's collection, pending-work snapshot and recovery activity. Each component is
    independently validated against the same original scrape target.

    """

    state: ObsRuntimeHandoffItemState
    collection: ObsRuntimeHandoffCollection
    """Freshness and shape of the collection-status gauges. Available metadata can report unsuccessful or expired
    collection; that never establishes an empty backlog."""
    backlog: ObsRuntimeHandoffBacklog
    """Waiting or known-empty snapshot evidence requires successful fresh collection after the evidence boundary
    and coherent original sample clocks. Missing evidence never becomes zero work."""
    recovery: ObsRuntimeHandoffRecovery
    """Sampled recovery-pass activity with no new progress timeout. Synchronous image work can legitimately occupy
    a long pass; stopping can retain an in-flight pass and does not prove drained work."""
    node: str | Unset = UNSET
    target_id: str | Unset = UNSET
    unexpected: bool | Unset = UNSET
    scrape_observed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        collection = self.collection.to_dict()

        backlog = self.backlog.to_dict()

        recovery = self.recovery.to_dict()

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
                "backlog": backlog,
                "recovery": recovery,
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
        from ..models.obs_runtime_handoff_backlog import ObsRuntimeHandoffBacklog
        from ..models.obs_runtime_handoff_collection import ObsRuntimeHandoffCollection
        from ..models.obs_runtime_handoff_recovery import ObsRuntimeHandoffRecovery

        d = dict(src_dict)
        state = check_obs_runtime_handoff_item_state(d.pop("state"))

        collection = ObsRuntimeHandoffCollection.from_dict(d.pop("collection"))

        backlog = ObsRuntimeHandoffBacklog.from_dict(d.pop("backlog"))

        recovery = ObsRuntimeHandoffRecovery.from_dict(d.pop("recovery"))

        node = d.pop("node", UNSET)

        target_id = d.pop("target_id", UNSET)

        unexpected = d.pop("unexpected", UNSET)

        _scrape_observed_at = d.pop("scrape_observed_at", UNSET)
        scrape_observed_at: datetime.datetime | Unset
        if isinstance(_scrape_observed_at, Unset):
            scrape_observed_at = UNSET
        else:
            scrape_observed_at = datetime.datetime.fromisoformat(_scrape_observed_at)

        obs_runtime_handoff_item = cls(
            state=state,
            collection=collection,
            backlog=backlog,
            recovery=recovery,
            node=node,
            target_id=target_id,
            unexpected=unexpected,
            scrape_observed_at=scrape_observed_at,
        )

        obs_runtime_handoff_item.additional_properties = d
        return obs_runtime_handoff_item

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
