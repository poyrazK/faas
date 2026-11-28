from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_promotion_migration_policy import ProjectPromotionMigrationPolicy
    from ..models.project_release_probe import ProjectReleaseProbe
    from ..models.project_release_worker import ProjectReleaseWorker


T = TypeVar("T", bound="ProjectPromotionHealthPolicy")


@_attrs_define
class ProjectPromotionHealthPolicy:
    """Destination probe and optional migration contract bound to the exact promotion preview and approval."""

    probes: list[ProjectReleaseProbe]
    workers: list[ProjectReleaseWorker] | Unset = UNSET
    """Complete immutable worker set for multiple-worker promotions; mutually exclusive with worker."""
    worker: ProjectReleaseWorker | Unset = UNSET
    """One graph-owned production push worker, its immutable binding intent and runtime admission evidence."""
    migration: ProjectPromotionMigrationPolicy | Unset = UNSET
    """One backward-compatible source release command authorized against destination runtime inputs. Contains a
    command fingerprint, never command bytes or credentials."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        probes = []
        for probes_item_data in self.probes:
            probes_item = probes_item_data.to_dict()
            probes.append(probes_item)

        workers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.workers, Unset):
            workers = []
            for workers_item_data in self.workers:
                workers_item = workers_item_data.to_dict()
                workers.append(workers_item)

        worker: dict[str, Any] | Unset = UNSET
        if not isinstance(self.worker, Unset):
            worker = self.worker.to_dict()

        migration: dict[str, Any] | Unset = UNSET
        if not isinstance(self.migration, Unset):
            migration = self.migration.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "probes": probes,
            }
        )
        if workers is not UNSET:
            field_dict["workers"] = workers
        if worker is not UNSET:
            field_dict["worker"] = worker
        if migration is not UNSET:
            field_dict["migration"] = migration

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_promotion_migration_policy import ProjectPromotionMigrationPolicy
        from ..models.project_release_probe import ProjectReleaseProbe
        from ..models.project_release_worker import ProjectReleaseWorker

        d = dict(src_dict)
        probes = []
        _probes = d.pop("probes")
        for probes_item_data in _probes:
            probes_item = ProjectReleaseProbe.from_dict(probes_item_data)

            probes.append(probes_item)

        _workers = d.pop("workers", UNSET)
        workers: list[ProjectReleaseWorker] | Unset = UNSET
        if _workers is not UNSET:
            workers = []
            for workers_item_data in _workers:
                workers_item = ProjectReleaseWorker.from_dict(workers_item_data)

                workers.append(workers_item)

        _worker = d.pop("worker", UNSET)
        worker: ProjectReleaseWorker | Unset
        if isinstance(_worker, Unset):
            worker = UNSET
        else:
            worker = ProjectReleaseWorker.from_dict(_worker)

        _migration = d.pop("migration", UNSET)
        migration: ProjectPromotionMigrationPolicy | Unset
        if isinstance(_migration, Unset):
            migration = UNSET
        else:
            migration = ProjectPromotionMigrationPolicy.from_dict(_migration)

        project_promotion_health_policy = cls(
            probes=probes,
            workers=workers,
            worker=worker,
            migration=migration,
        )

        project_promotion_health_policy.additional_properties = d
        return project_promotion_health_policy

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
