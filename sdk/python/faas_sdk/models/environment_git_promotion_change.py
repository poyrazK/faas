from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.environment_workload_source import EnvironmentWorkloadSource


T = TypeVar("T", bound="EnvironmentGitPromotionChange")


@_attrs_define
class EnvironmentGitPromotionChange:
    """Only a workload source contract changes; target configuration and identity are preserved."""

    resource: str
    """Target workload resource whose source contract is proposed to change."""
    before: EnvironmentWorkloadSource
    """Build source within the approved Git tree, a function runner, or an immutable OCI digest. Function sources
    require runtime and exclude dockerfile. An immutable image may explicitly opt into full-rootfs dispatch. The
    gated internal executor reserves new private workloads when app is omitted. Preparation does not grant serving
    authority."""
    after: EnvironmentWorkloadSource
    """Build source within the approved Git tree, a function runner, or an immutable OCI digest. Function sources
    require runtime and exclude dockerfile. An immutable image may explicitly opt into full-rootfs dispatch. The
    gated internal executor reserves new private workloads when app is omitted. Preparation does not grant serving
    authority."""

    def to_dict(self) -> dict[str, Any]:
        resource = self.resource

        before = self.before.to_dict()

        after = self.after.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "resource": resource,
                "before": before,
                "after": after,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_workload_source import EnvironmentWorkloadSource

        d = dict(src_dict)
        resource = d.pop("resource")

        before = EnvironmentWorkloadSource.from_dict(d.pop("before"))

        after = EnvironmentWorkloadSource.from_dict(d.pop("after"))

        environment_git_promotion_change = cls(
            resource=resource,
            before=before,
            after=after,
        )

        return environment_git_promotion_change
