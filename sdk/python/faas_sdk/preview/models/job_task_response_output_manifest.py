from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.job_task_response_output_manifest_version import (
    JobTaskResponseOutputManifestVersion,
    check_job_task_response_output_manifest_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.job_task_response_output_manifest_artifacts_item import JobTaskResponseOutputManifestArtifactsItem


T = TypeVar("T", bound="JobTaskResponseOutputManifest")


@_attrs_define
class JobTaskResponseOutputManifest:
    """Versioned artifact references published by a successful task. Gregale retains metadata, not artifact bytes."""

    version: JobTaskResponseOutputManifestVersion | Unset = UNSET
    artifacts: list[JobTaskResponseOutputManifestArtifactsItem] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int | Unset = UNSET
        if not isinstance(self.version, Unset):
            version = self.version

        artifacts: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.artifacts, Unset):
            artifacts = []
            for artifacts_item_data in self.artifacts:
                artifacts_item = artifacts_item_data.to_dict()
                artifacts.append(artifacts_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if version is not UNSET:
            field_dict["version"] = version
        if artifacts is not UNSET:
            field_dict["artifacts"] = artifacts

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.job_task_response_output_manifest_artifacts_item import JobTaskResponseOutputManifestArtifactsItem

        d = dict(src_dict)
        _version = d.pop("version", UNSET)
        version: JobTaskResponseOutputManifestVersion | Unset
        if isinstance(_version, Unset):
            version = UNSET
        else:
            version = check_job_task_response_output_manifest_version(_version)

        _artifacts = d.pop("artifacts", UNSET)
        artifacts: list[JobTaskResponseOutputManifestArtifactsItem] | Unset = UNSET
        if _artifacts is not UNSET:
            artifacts = []
            for artifacts_item_data in _artifacts:
                artifacts_item = JobTaskResponseOutputManifestArtifactsItem.from_dict(artifacts_item_data)

                artifacts.append(artifacts_item)

        job_task_response_output_manifest = cls(
            version=version,
            artifacts=artifacts,
        )

        job_task_response_output_manifest.additional_properties = d
        return job_task_response_output_manifest

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
