from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_release_workload_response_status import (
    ProjectEnvironmentReleaseWorkloadResponseStatus,
    check_project_environment_release_workload_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectEnvironmentReleaseWorkloadResponse")


@_attrs_define
class ProjectEnvironmentReleaseWorkloadResponse:
    """Current live deployment metadata for one project workload."""

    workload_slug: str
    workload_name: str
    status: ProjectEnvironmentReleaseWorkloadResponseStatus
    deployment_id: str | Unset = UNSET
    build_id: str | Unset = UNSET
    image_digest: str | Unset = UNSET
    source_url: str | Unset = UNSET
    commit_sha: str | Unset = UNSET
    source_sha256: str | Unset = UNSET
    traffic_percent: int | Unset = UNSET
    created_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workload_slug = self.workload_slug

        workload_name = self.workload_name

        status: str = self.status

        deployment_id = self.deployment_id

        build_id = self.build_id

        image_digest = self.image_digest

        source_url = self.source_url

        commit_sha = self.commit_sha

        source_sha256 = self.source_sha256

        traffic_percent = self.traffic_percent

        created_at: str | Unset = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workload_slug": workload_slug,
                "workload_name": workload_name,
                "status": status,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if build_id is not UNSET:
            field_dict["build_id"] = build_id
        if image_digest is not UNSET:
            field_dict["image_digest"] = image_digest
        if source_url is not UNSET:
            field_dict["source_url"] = source_url
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if source_sha256 is not UNSET:
            field_dict["source_sha256"] = source_sha256
        if traffic_percent is not UNSET:
            field_dict["traffic_percent"] = traffic_percent
        if created_at is not UNSET:
            field_dict["created_at"] = created_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workload_slug = d.pop("workload_slug")

        workload_name = d.pop("workload_name")

        status = check_project_environment_release_workload_response_status(d.pop("status"))

        deployment_id = d.pop("deployment_id", UNSET)

        build_id = d.pop("build_id", UNSET)

        image_digest = d.pop("image_digest", UNSET)

        source_url = d.pop("source_url", UNSET)

        commit_sha = d.pop("commit_sha", UNSET)

        source_sha256 = d.pop("source_sha256", UNSET)

        traffic_percent = d.pop("traffic_percent", UNSET)

        _created_at = d.pop("created_at", UNSET)
        created_at: datetime.datetime | Unset
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = datetime.datetime.fromisoformat(_created_at)

        project_environment_release_workload_response = cls(
            workload_slug=workload_slug,
            workload_name=workload_name,
            status=status,
            deployment_id=deployment_id,
            build_id=build_id,
            image_digest=image_digest,
            source_url=source_url,
            commit_sha=commit_sha,
            source_sha256=source_sha256,
            traffic_percent=traffic_percent,
            created_at=created_at,
        )

        project_environment_release_workload_response.additional_properties = d
        return project_environment_release_workload_response

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
