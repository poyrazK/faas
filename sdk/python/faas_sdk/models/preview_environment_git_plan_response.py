from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.preview_environment_git_plan_response_provider import (
    PreviewEnvironmentGitPlanResponseProvider,
    check_preview_environment_git_plan_response_provider,
)

if TYPE_CHECKING:
    from ..models.environment_git_ops_plan import EnvironmentGitOpsPlan


T = TypeVar("T", bound="PreviewEnvironmentGitPlanResponse")


@_attrs_define
class PreviewEnvironmentGitPlanResponse:
    """Read-only candidate plan pinned to a source generation and observed intent version. Values are omitted. The plan
    cannot authorize approval, adoption, deployment or runtime readiness. Re-run after source or environment changes.

    """

    source_id: UUID
    provider: PreviewEnvironmentGitPlanResponseProvider
    project: str
    environment: str
    observed_at: datetime.datetime
    read_only: bool
    values_redacted: bool
    plan: EnvironmentGitOpsPlan
    """Observation-bound plan; blocking reasons prevent mutation and overrides prevent convergence."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source_id = str(self.source_id)

        provider: str = self.provider

        project = self.project

        environment = self.environment

        observed_at = self.observed_at.isoformat()

        read_only = self.read_only

        values_redacted = self.values_redacted

        plan = self.plan.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source_id": source_id,
                "provider": provider,
                "project": project,
                "environment": environment,
                "observed_at": observed_at,
                "read_only": read_only,
                "values_redacted": values_redacted,
                "plan": plan,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_git_ops_plan import EnvironmentGitOpsPlan

        d = dict(src_dict)
        source_id = UUID(d.pop("source_id"))

        provider = check_preview_environment_git_plan_response_provider(d.pop("provider"))

        project = d.pop("project")

        environment = d.pop("environment")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        read_only = d.pop("read_only")

        values_redacted = d.pop("values_redacted")

        plan = EnvironmentGitOpsPlan.from_dict(d.pop("plan"))

        preview_environment_git_plan_response = cls(
            source_id=source_id,
            provider=provider,
            project=project,
            environment=environment,
            observed_at=observed_at,
            read_only=read_only,
            values_redacted=values_redacted,
            plan=plan,
        )

        preview_environment_git_plan_response.additional_properties = d
        return preview_environment_git_plan_response

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
