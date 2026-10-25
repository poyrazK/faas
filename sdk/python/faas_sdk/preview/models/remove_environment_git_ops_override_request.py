from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="RemoveEnvironmentGitOpsOverrideRequest")


@_attrs_define
class RemoveEnvironmentGitOpsOverrideRequest:
    """Owned field whose temporary override should be revoked."""

    resource: str
    path: str

    def to_dict(self) -> dict[str, Any]:
        resource = self.resource

        path = self.path

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "resource": resource,
                "path": path,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        resource = d.pop("resource")

        path = d.pop("path")

        remove_environment_git_ops_override_request = cls(
            resource=resource,
            path=path,
        )

        return remove_environment_git_ops_override_request
