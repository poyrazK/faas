from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="DeploymentResponseCheckResult")


@_attrs_define
class DeploymentResponseCheckResult:
    """Ephemeral owner-authorized HTTP evidence; does not establish public ingress access."""

    app_id: str
    deployment_id: str
    scope: str
    instance_id: str
    http_status: int
    body: str
    """UTF-8 response preview bounded to 4096 bytes."""
    body_truncated: bool
    project_id: str | Unset = UNSET
    environment_id: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        deployment_id = self.deployment_id

        scope = self.scope

        instance_id = self.instance_id

        http_status = self.http_status

        body = self.body

        body_truncated = self.body_truncated

        project_id = self.project_id

        environment_id = self.environment_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "deployment_id": deployment_id,
                "scope": scope,
                "instance_id": instance_id,
                "http_status": http_status,
                "body": body,
                "body_truncated": body_truncated,
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

        deployment_id = d.pop("deployment_id")

        scope = d.pop("scope")

        instance_id = d.pop("instance_id")

        http_status = d.pop("http_status")

        body = d.pop("body")

        body_truncated = d.pop("body_truncated")

        project_id = d.pop("project_id", UNSET)

        environment_id = d.pop("environment_id", UNSET)

        deployment_response_check_result = cls(
            app_id=app_id,
            deployment_id=deployment_id,
            scope=scope,
            instance_id=instance_id,
            http_status=http_status,
            body=body,
            body_truncated=body_truncated,
            project_id=project_id,
            environment_id=environment_id,
        )

        return deployment_response_check_result
