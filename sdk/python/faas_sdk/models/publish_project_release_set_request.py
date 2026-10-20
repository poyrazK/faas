from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.publish_project_release_set_request_deployments import PublishProjectReleaseSetRequestDeployments


T = TypeVar("T", bound="PublishProjectReleaseSetRequest")


@_attrs_define
class PublishProjectReleaseSetRequest:
    """A complete mapping of project workload slugs to live deployment UUIDs."""

    ttl_seconds: int
    deployments: PublishProjectReleaseSetRequestDeployments

    def to_dict(self) -> dict[str, Any]:
        ttl_seconds = self.ttl_seconds

        deployments = self.deployments.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "ttl_seconds": ttl_seconds,
                "deployments": deployments,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.publish_project_release_set_request_deployments import PublishProjectReleaseSetRequestDeployments

        d = dict(src_dict)
        ttl_seconds = d.pop("ttl_seconds")

        deployments = PublishProjectReleaseSetRequestDeployments.from_dict(d.pop("deployments"))

        publish_project_release_set_request = cls(
            ttl_seconds=ttl_seconds,
            deployments=deployments,
        )

        return publish_project_release_set_request
