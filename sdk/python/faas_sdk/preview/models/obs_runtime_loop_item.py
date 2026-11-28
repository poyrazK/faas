from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_loop_item_loop import ObsRuntimeLoopItemLoop, check_obs_runtime_loop_item_loop
from ..models.obs_runtime_loop_item_reason import ObsRuntimeLoopItemReason, check_obs_runtime_loop_item_reason
from ..models.obs_runtime_loop_item_state import ObsRuntimeLoopItemState, check_obs_runtime_loop_item_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuntimeLoopItem")


@_attrs_define
class ObsRuntimeLoopItem:
    """Sanitized loop observation with original scrape and metric clocks; loop names are selected explicitly per daemon.
    Age is diagnostic and the producer owns its stall budget.

    """

    job: str
    daemon: str
    loop: ObsRuntimeLoopItemLoop
    state: ObsRuntimeLoopItemState
    node: str | Unset = UNSET
    target_id: str | Unset = UNSET
    unexpected: bool | Unset = UNSET
    reason: ObsRuntimeLoopItemReason | Unset = UNSET
    reported_stalled: bool | Unset = UNSET
    age_seconds: float | Unset = UNSET
    scrape_observed_at: datetime.datetime | Unset = UNSET
    stall_observed_at: datetime.datetime | Unset = UNSET
    age_observed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        job = self.job

        daemon = self.daemon

        loop: str = self.loop

        state: str = self.state

        node = self.node

        target_id = self.target_id

        unexpected = self.unexpected

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        reported_stalled = self.reported_stalled

        age_seconds = self.age_seconds

        scrape_observed_at: str | Unset = UNSET
        if not isinstance(self.scrape_observed_at, Unset):
            scrape_observed_at = self.scrape_observed_at.isoformat()

        stall_observed_at: str | Unset = UNSET
        if not isinstance(self.stall_observed_at, Unset):
            stall_observed_at = self.stall_observed_at.isoformat()

        age_observed_at: str | Unset = UNSET
        if not isinstance(self.age_observed_at, Unset):
            age_observed_at = self.age_observed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "job": job,
                "daemon": daemon,
                "loop": loop,
                "state": state,
            }
        )
        if node is not UNSET:
            field_dict["node"] = node
        if target_id is not UNSET:
            field_dict["target_id"] = target_id
        if unexpected is not UNSET:
            field_dict["unexpected"] = unexpected
        if reason is not UNSET:
            field_dict["reason"] = reason
        if reported_stalled is not UNSET:
            field_dict["reported_stalled"] = reported_stalled
        if age_seconds is not UNSET:
            field_dict["age_seconds"] = age_seconds
        if scrape_observed_at is not UNSET:
            field_dict["scrape_observed_at"] = scrape_observed_at
        if stall_observed_at is not UNSET:
            field_dict["stall_observed_at"] = stall_observed_at
        if age_observed_at is not UNSET:
            field_dict["age_observed_at"] = age_observed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        job = d.pop("job")

        daemon = d.pop("daemon")

        loop = check_obs_runtime_loop_item_loop(d.pop("loop"))

        state = check_obs_runtime_loop_item_state(d.pop("state"))

        node = d.pop("node", UNSET)

        target_id = d.pop("target_id", UNSET)

        unexpected = d.pop("unexpected", UNSET)

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeLoopItemReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_loop_item_reason(_reason)

        reported_stalled = d.pop("reported_stalled", UNSET)

        age_seconds = d.pop("age_seconds", UNSET)

        _scrape_observed_at = d.pop("scrape_observed_at", UNSET)
        scrape_observed_at: datetime.datetime | Unset
        if isinstance(_scrape_observed_at, Unset):
            scrape_observed_at = UNSET
        else:
            scrape_observed_at = datetime.datetime.fromisoformat(_scrape_observed_at)

        _stall_observed_at = d.pop("stall_observed_at", UNSET)
        stall_observed_at: datetime.datetime | Unset
        if isinstance(_stall_observed_at, Unset):
            stall_observed_at = UNSET
        else:
            stall_observed_at = datetime.datetime.fromisoformat(_stall_observed_at)

        _age_observed_at = d.pop("age_observed_at", UNSET)
        age_observed_at: datetime.datetime | Unset
        if isinstance(_age_observed_at, Unset):
            age_observed_at = UNSET
        else:
            age_observed_at = datetime.datetime.fromisoformat(_age_observed_at)

        obs_runtime_loop_item = cls(
            job=job,
            daemon=daemon,
            loop=loop,
            state=state,
            node=node,
            target_id=target_id,
            unexpected=unexpected,
            reason=reason,
            reported_stalled=reported_stalled,
            age_seconds=age_seconds,
            scrape_observed_at=scrape_observed_at,
            stall_observed_at=stall_observed_at,
            age_observed_at=age_observed_at,
        )

        obs_runtime_loop_item.additional_properties = d
        return obs_runtime_loop_item

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
