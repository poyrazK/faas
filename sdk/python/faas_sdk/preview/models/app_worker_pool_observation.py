from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.worker_admission_blocker import WorkerAdmissionBlocker


T = TypeVar("T", bound="AppWorkerPoolObservation")


@_attrs_define
class AppWorkerPoolObservation:
    """Last scheduler target after environment scaling and cooldowns, before account or physical capacity admission. Counts
    describe that observation time; stale after fifteen minutes. Running counts only the target generation; predecessor
    and stopping residents count as draining.

    """

    scope: str
    observed_at: datetime.datetime
    stale: bool
    desired: int
    running: int
    starting: int
    draining: int
    deployment_id: str | Unset = UNSET
    blockers: list[WorkerAdmissionBlocker] | Unset = UNSET
    """Last actual admission refusals for this pool. Absent evidence is unknown. A cooldown can retain earlier
    refusals with their original timestamps; these are not a fresh capacity forecast. Cleared when target slots are
    satisfied."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope = self.scope

        observed_at = self.observed_at.isoformat()

        stale = self.stale

        desired = self.desired

        running = self.running

        starting = self.starting

        draining = self.draining

        deployment_id = self.deployment_id

        blockers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.blockers, Unset):
            blockers = []
            for blockers_item_data in self.blockers:
                blockers_item = blockers_item_data.to_dict()
                blockers.append(blockers_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scope": scope,
                "observed_at": observed_at,
                "stale": stale,
                "desired": desired,
                "running": running,
                "starting": starting,
                "draining": draining,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if blockers is not UNSET:
            field_dict["blockers"] = blockers

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.worker_admission_blocker import WorkerAdmissionBlocker

        d = dict(src_dict)
        scope = d.pop("scope")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        stale = d.pop("stale")

        desired = d.pop("desired")

        running = d.pop("running")

        starting = d.pop("starting")

        draining = d.pop("draining")

        deployment_id = d.pop("deployment_id", UNSET)

        _blockers = d.pop("blockers", UNSET)
        blockers: list[WorkerAdmissionBlocker] | Unset = UNSET
        if _blockers is not UNSET:
            blockers = []
            for blockers_item_data in _blockers:
                blockers_item = WorkerAdmissionBlocker.from_dict(blockers_item_data)

                blockers.append(blockers_item)

        app_worker_pool_observation = cls(
            scope=scope,
            observed_at=observed_at,
            stale=stale,
            desired=desired,
            running=running,
            starting=starting,
            draining=draining,
            deployment_id=deployment_id,
            blockers=blockers,
        )

        app_worker_pool_observation.additional_properties = d
        return app_worker_pool_observation

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
