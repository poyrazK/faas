from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

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

    def to_dict(self) -> dict[str, Any]:
        resource = self.resource

        deployment_id = self.deployment_id

        kind = self.kind

        build_id = self.build_id

        image_digest = self.image_digest

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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        resource = d.pop("resource")

        deployment_id = d.pop("deployment_id")

        kind = d.pop("kind")

        build_id = d.pop("build_id", UNSET)

        image_digest = d.pop("image_digest", UNSET)

        environment_git_promotion_artifact = cls(
            resource=resource,
            deployment_id=deployment_id,
            kind=kind,
            build_id=build_id,
            image_digest=image_digest,
        )

        return environment_git_promotion_artifact
