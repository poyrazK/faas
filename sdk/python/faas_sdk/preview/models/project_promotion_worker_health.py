from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_release_worker import ProjectReleaseWorker
    from ..models.project_worker_health import ProjectWorkerHealth


T = TypeVar("T", bound="ProjectPromotionWorkerHealth")


@_attrs_define
class ProjectPromotionWorkerHealth:
    """Captured queue worker policy paired with observed delivery health for one promotion member."""

    worker: ProjectReleaseWorker
    """One graph-owned production push worker, its immutable binding intent and runtime admission evidence."""
    health: ProjectWorkerHealth
    """Opt-in real production queue delivery verification; absence of traffic never proves health."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        worker = self.worker.to_dict()

        health = self.health.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "worker": worker,
                "health": health,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_release_worker import ProjectReleaseWorker
        from ..models.project_worker_health import ProjectWorkerHealth

        d = dict(src_dict)
        worker = ProjectReleaseWorker.from_dict(d.pop("worker"))

        health = ProjectWorkerHealth.from_dict(d.pop("health"))

        project_promotion_worker_health = cls(
            worker=worker,
            health=health,
        )

        project_promotion_worker_health.additional_properties = d
        return project_promotion_worker_health

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
