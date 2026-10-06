from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.publish_project_release_set_request_deployments import PublishProjectReleaseSetRequestDeployments


T = TypeVar("T", bound="PublishProjectReleaseSetRequest")


@_attrs_define
class PublishProjectReleaseSetRequest:
    """A complete mapping of project workload slugs to live deployment UUIDs."""

    ttl_seconds: int
    deployments: PublishProjectReleaseSetRequestDeployments
    expected_active_release_id: str | Unset = UNSET
    """Current release UUID, or empty for no graph. Presence selects checked activation; mandatory on the check
    route."""

    def to_dict(self) -> dict[str, Any]:
        ttl_seconds = self.ttl_seconds

        deployments = self.deployments.to_dict()

        expected_active_release_id = self.expected_active_release_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "ttl_seconds": ttl_seconds,
                "deployments": deployments,
            }
        )
        if expected_active_release_id is not UNSET:
            field_dict["expected_active_release_id"] = expected_active_release_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.publish_project_release_set_request_deployments import PublishProjectReleaseSetRequestDeployments

        d = dict(src_dict)
        ttl_seconds = d.pop("ttl_seconds")

        deployments = PublishProjectReleaseSetRequestDeployments.from_dict(d.pop("deployments"))

        expected_active_release_id = d.pop("expected_active_release_id", UNSET)

        publish_project_release_set_request = cls(
            ttl_seconds=ttl_seconds,
            deployments=deployments,
            expected_active_release_id=expected_active_release_id,
        )

        return publish_project_release_set_request
