from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_build_item_state import ObsRuntimeBuildItemState, check_obs_runtime_build_item_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_process_item import ObsRuntimeProcessItem
    from ..models.obs_runtime_readiness_item import ObsRuntimeReadinessItem


T = TypeVar("T", bound="ObsRuntimeBuildItem")


@_attrs_define
class ObsRuntimeBuildItem:
    """Sanitized target observation or missing expectation; a SHA is present only on fresh unambiguous build evidence."""

    job: str
    daemon: str
    state: ObsRuntimeBuildItemState
    node: str | Unset = UNSET
    target_id: str | Unset = UNSET
    reason: str | Unset = UNSET
    unexpected: bool | Unset = UNSET
    git_sha: str | Unset = UNSET
    version: str | Unset = UNSET
    observed_at: datetime.datetime | Unset = UNSET
    scrape_observed_at: datetime.datetime | Unset = UNSET
    readiness: ObsRuntimeReadinessItem | Unset = UNSET
    """Original readiness and active reason sample clocks; ready requires a fresh successful scrape and complete
    consistent one-hot reasons."""
    process: ObsRuntimeProcessItem | Unset = UNSET
    """Original uptime and sample clocks; comparison uses the same complete target identity and permits clock skew.
    Optional fields are absent for unavailable evidence."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        job = self.job

        daemon = self.daemon

        state: str = self.state

        node = self.node

        target_id = self.target_id

        reason = self.reason

        unexpected = self.unexpected

        git_sha = self.git_sha

        version = self.version

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        scrape_observed_at: str | Unset = UNSET
        if not isinstance(self.scrape_observed_at, Unset):
            scrape_observed_at = self.scrape_observed_at.isoformat()

        readiness: dict[str, Any] | Unset = UNSET
        if not isinstance(self.readiness, Unset):
            readiness = self.readiness.to_dict()

        process: dict[str, Any] | Unset = UNSET
        if not isinstance(self.process, Unset):
            process = self.process.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "job": job,
                "daemon": daemon,
                "state": state,
            }
        )
        if node is not UNSET:
            field_dict["node"] = node
        if target_id is not UNSET:
            field_dict["target_id"] = target_id
        if reason is not UNSET:
            field_dict["reason"] = reason
        if unexpected is not UNSET:
            field_dict["unexpected"] = unexpected
        if git_sha is not UNSET:
            field_dict["git_sha"] = git_sha
        if version is not UNSET:
            field_dict["version"] = version
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if scrape_observed_at is not UNSET:
            field_dict["scrape_observed_at"] = scrape_observed_at
        if readiness is not UNSET:
            field_dict["readiness"] = readiness
        if process is not UNSET:
            field_dict["process"] = process

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_runtime_process_item import ObsRuntimeProcessItem
        from ..models.obs_runtime_readiness_item import ObsRuntimeReadinessItem

        d = dict(src_dict)
        job = d.pop("job")

        daemon = d.pop("daemon")

        state = check_obs_runtime_build_item_state(d.pop("state"))

        node = d.pop("node", UNSET)

        target_id = d.pop("target_id", UNSET)

        reason = d.pop("reason", UNSET)

        unexpected = d.pop("unexpected", UNSET)

        git_sha = d.pop("git_sha", UNSET)

        version = d.pop("version", UNSET)

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        _scrape_observed_at = d.pop("scrape_observed_at", UNSET)
        scrape_observed_at: datetime.datetime | Unset
        if isinstance(_scrape_observed_at, Unset):
            scrape_observed_at = UNSET
        else:
            scrape_observed_at = datetime.datetime.fromisoformat(_scrape_observed_at)

        _readiness = d.pop("readiness", UNSET)
        readiness: ObsRuntimeReadinessItem | Unset
        if isinstance(_readiness, Unset):
            readiness = UNSET
        else:
            readiness = ObsRuntimeReadinessItem.from_dict(_readiness)

        _process = d.pop("process", UNSET)
        process: ObsRuntimeProcessItem | Unset
        if isinstance(_process, Unset):
            process = UNSET
        else:
            process = ObsRuntimeProcessItem.from_dict(_process)

        obs_runtime_build_item = cls(
            job=job,
            daemon=daemon,
            state=state,
            node=node,
            target_id=target_id,
            reason=reason,
            unexpected=unexpected,
            git_sha=git_sha,
            version=version,
            observed_at=observed_at,
            scrape_observed_at=scrape_observed_at,
            readiness=readiness,
            process=process,
        )

        obs_runtime_build_item.additional_properties = d
        return obs_runtime_build_item

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
