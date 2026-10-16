from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_release_worker_status import ProjectReleaseWorkerStatus, check_project_release_worker_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectReleaseWorker")


@_attrs_define
class ProjectReleaseWorker:
    """One graph-owned production push worker, its immutable binding intent and runtime admission evidence."""

    app_id: str
    slug: str
    binding_id: str
    binding_hash: str
    previous_deployment_id: str
    drain_seconds: int
    status: ProjectReleaseWorkerStatus
    deployment_id: str | Unset = UNSET
    instance_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        slug = self.slug

        binding_id = self.binding_id

        binding_hash = self.binding_hash

        previous_deployment_id = self.previous_deployment_id

        drain_seconds = self.drain_seconds

        status: str = self.status

        deployment_id = self.deployment_id

        instance_id = self.instance_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "slug": slug,
                "binding_id": binding_id,
                "binding_hash": binding_hash,
                "previous_deployment_id": previous_deployment_id,
                "drain_seconds": drain_seconds,
                "status": status,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        slug = d.pop("slug")

        binding_id = d.pop("binding_id")

        binding_hash = d.pop("binding_hash")

        previous_deployment_id = d.pop("previous_deployment_id")

        drain_seconds = d.pop("drain_seconds")

        status = check_project_release_worker_status(d.pop("status"))

        deployment_id = d.pop("deployment_id", UNSET)

        instance_id = d.pop("instance_id", UNSET)

        project_release_worker = cls(
            app_id=app_id,
            slug=slug,
            binding_id=binding_id,
            binding_hash=binding_hash,
            previous_deployment_id=previous_deployment_id,
            drain_seconds=drain_seconds,
            status=status,
            deployment_id=deployment_id,
            instance_id=instance_id,
        )

        project_release_worker.additional_properties = d
        return project_release_worker

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
