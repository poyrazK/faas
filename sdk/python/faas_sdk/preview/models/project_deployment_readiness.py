from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_deployment_readiness_schema_version import (
    ProjectDeploymentReadinessSchemaVersion,
    check_project_deployment_readiness_schema_version,
)
from ..models.project_deployment_readiness_status import (
    ProjectDeploymentReadinessStatus,
    check_project_deployment_readiness_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_deployment_check import ProjectDeploymentCheck


T = TypeVar("T", bound="ProjectDeploymentReadiness")


@_attrs_define
class ProjectDeploymentReadiness:
    """Metadata-only project deployment readiness report tied to the inspected environment and state hash."""

    schema_version: ProjectDeploymentReadinessSchemaVersion
    project_slug: str
    environment: str
    status: ProjectDeploymentReadinessStatus
    checked_at: datetime.datetime
    runtime_checked: bool
    checks: list[ProjectDeploymentCheck]
    from_environment: str | Unset = UNSET
    state_hash: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        schema_version: int = self.schema_version

        project_slug = self.project_slug

        environment = self.environment

        status: str = self.status

        checked_at = self.checked_at.isoformat()

        runtime_checked = self.runtime_checked

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        from_environment = self.from_environment

        state_hash = self.state_hash

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "schema_version": schema_version,
                "project_slug": project_slug,
                "environment": environment,
                "status": status,
                "checked_at": checked_at,
                "runtime_checked": runtime_checked,
                "checks": checks,
            }
        )
        if from_environment is not UNSET:
            field_dict["from_environment"] = from_environment
        if state_hash is not UNSET:
            field_dict["state_hash"] = state_hash

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_deployment_check import ProjectDeploymentCheck

        d = dict(src_dict)
        schema_version = check_project_deployment_readiness_schema_version(d.pop("schema_version"))

        project_slug = d.pop("project_slug")

        environment = d.pop("environment")

        status = check_project_deployment_readiness_status(d.pop("status"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        runtime_checked = d.pop("runtime_checked")

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = ProjectDeploymentCheck.from_dict(checks_item_data)

            checks.append(checks_item)

        from_environment = d.pop("from_environment", UNSET)

        state_hash = d.pop("state_hash", UNSET)

        project_deployment_readiness = cls(
            schema_version=schema_version,
            project_slug=project_slug,
            environment=environment,
            status=status,
            checked_at=checked_at,
            runtime_checked=runtime_checked,
            checks=checks,
            from_environment=from_environment,
            state_hash=state_hash,
        )

        project_deployment_readiness.additional_properties = d
        return project_deployment_readiness

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
