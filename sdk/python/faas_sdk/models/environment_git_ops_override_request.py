from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="EnvironmentGitOpsOverrideRequest")


@_attrs_define
class EnvironmentGitOpsOverrideRequest:
    """Temporary permission to edit one Git-owned field, bounded to twenty-four hours."""

    resource: str
    path: str
    reason: str
    expires_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        resource = self.resource

        path = self.path

        reason = self.reason

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "resource": resource,
                "path": path,
                "reason": reason,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        resource = d.pop("resource")

        path = d.pop("path")

        reason = d.pop("reason")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        environment_git_ops_override_request = cls(
            resource=resource,
            path=path,
            reason=reason,
            expires_at=expires_at,
        )

        return environment_git_ops_override_request
