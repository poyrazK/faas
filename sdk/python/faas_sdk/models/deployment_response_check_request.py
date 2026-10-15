from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="DeploymentResponseCheckRequest")


@_attrs_define
class DeploymentResponseCheckRequest:
    """Explicit identity and one GET path; named scopes require project_id and environment_id."""

    app_id: str
    scope: str
    path: str
    """App-relative GET path with optional query; no fragment or controls."""
    project_id: str | Unset = UNSET
    environment_id: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        scope = self.scope

        path = self.path

        project_id = self.project_id

        environment_id = self.environment_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "scope": scope,
                "path": path,
            }
        )
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        scope = d.pop("scope")

        path = d.pop("path")

        project_id = d.pop("project_id", UNSET)

        environment_id = d.pop("environment_id", UNSET)

        deployment_response_check_request = cls(
            app_id=app_id,
            scope=scope,
            path=path,
            project_id=project_id,
            environment_id=environment_id,
        )

        return deployment_response_check_request
