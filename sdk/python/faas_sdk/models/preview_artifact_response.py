from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PreviewArtifactResponse")


@_attrs_define
class PreviewArtifactResponse:
    """Strongest available non-secret deployment artifact identity."""

    deployment_id: str | Unset = UNSET
    revision: int | Unset = UNSET
    status: str | Unset = UNSET
    image_digest: str | Unset = UNSET
    source_sha256: str | Unset = UNSET
    commit_sha: str | Unset = UNSET
    build_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = self.deployment_id

        revision = self.revision

        status = self.status

        image_digest = self.image_digest

        source_sha256 = self.source_sha256

        commit_sha = self.commit_sha

        build_id = self.build_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if revision is not UNSET:
            field_dict["revision"] = revision
        if status is not UNSET:
            field_dict["status"] = status
        if image_digest is not UNSET:
            field_dict["image_digest"] = image_digest
        if source_sha256 is not UNSET:
            field_dict["source_sha256"] = source_sha256
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if build_id is not UNSET:
            field_dict["build_id"] = build_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = d.pop("deployment_id", UNSET)

        revision = d.pop("revision", UNSET)

        status = d.pop("status", UNSET)

        image_digest = d.pop("image_digest", UNSET)

        source_sha256 = d.pop("source_sha256", UNSET)

        commit_sha = d.pop("commit_sha", UNSET)

        build_id = d.pop("build_id", UNSET)

        preview_artifact_response = cls(
            deployment_id=deployment_id,
            revision=revision,
            status=status,
            image_digest=image_digest,
            source_sha256=source_sha256,
            commit_sha=commit_sha,
            build_id=build_id,
        )

        preview_artifact_response.additional_properties = d
        return preview_artifact_response

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
