from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EnvironmentGitPromotionArtifact")


@_attrs_define
class EnvironmentGitPromotionArtifact:
    """Selected active source deployment identity, without host paths or storage keys."""

    resource: str
    """Logical workload resource."""
    deployment_id: str
    """Exact selected live deployment."""
    kind: str
    """Recorded deployment kind."""
    build_id: str | Unset = UNSET
    """Associated build identity, if present."""
    image_digest: str | Unset = UNSET
    """Recorded immutable image identity, if present."""
    build_artifact_id: UUID | Unset = UNSET
    """Existing signed source-build producer identity."""
    build_artifact_hash: str | Unset = UNSET
    """Immutable producer input hash."""
    artifact_digest: str | Unset = UNSET
    """Exact built application-layer byte digest."""
    build_commit_sha: str | Unset = UNSET
    """Original code commit, distinct from the target manifest commit."""

    def to_dict(self) -> dict[str, Any]:
        resource = self.resource

        deployment_id = self.deployment_id

        kind = self.kind

        build_id = self.build_id

        image_digest = self.image_digest

        build_artifact_id: str | Unset = UNSET
        if not isinstance(self.build_artifact_id, Unset):
            build_artifact_id = str(self.build_artifact_id)

        build_artifact_hash = self.build_artifact_hash

        artifact_digest = self.artifact_digest

        build_commit_sha = self.build_commit_sha

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "resource": resource,
                "deployment_id": deployment_id,
                "kind": kind,
            }
        )
        if build_id is not UNSET:
            field_dict["build_id"] = build_id
        if image_digest is not UNSET:
            field_dict["image_digest"] = image_digest
        if build_artifact_id is not UNSET:
            field_dict["build_artifact_id"] = build_artifact_id
        if build_artifact_hash is not UNSET:
            field_dict["build_artifact_hash"] = build_artifact_hash
        if artifact_digest is not UNSET:
            field_dict["artifact_digest"] = artifact_digest
        if build_commit_sha is not UNSET:
            field_dict["build_commit_sha"] = build_commit_sha

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        resource = d.pop("resource")

        deployment_id = d.pop("deployment_id")

        kind = d.pop("kind")

        build_id = d.pop("build_id", UNSET)

        image_digest = d.pop("image_digest", UNSET)

        _build_artifact_id = d.pop("build_artifact_id", UNSET)
        build_artifact_id: UUID | Unset
        if isinstance(_build_artifact_id, Unset):
            build_artifact_id = UNSET
        else:
            build_artifact_id = UUID(_build_artifact_id)

        build_artifact_hash = d.pop("build_artifact_hash", UNSET)

        artifact_digest = d.pop("artifact_digest", UNSET)

        build_commit_sha = d.pop("build_commit_sha", UNSET)

        environment_git_promotion_artifact = cls(
            resource=resource,
            deployment_id=deployment_id,
            kind=kind,
            build_id=build_id,
            image_digest=image_digest,
            build_artifact_id=build_artifact_id,
            build_artifact_hash=build_artifact_hash,
            artifact_digest=artifact_digest,
            build_commit_sha=build_commit_sha,
        )

        return environment_git_promotion_artifact
