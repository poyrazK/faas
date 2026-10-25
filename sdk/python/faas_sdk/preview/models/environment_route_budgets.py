from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.environment_route_budgets_budget_source import (
    EnvironmentRouteBudgetsBudgetSource,
    check_environment_route_budgets_budget_source,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_monitor_config import RouteMonitorConfig


T = TypeVar("T", bound="EnvironmentRouteBudgets")


@_attrs_define
class EnvironmentRouteBudgets:
    """Effective advisory route budgets for one immutable app environment."""

    app_id: UUID
    scope: str
    budget_source: EnvironmentRouteBudgetsBudgetSource
    revision: int
    app_revision: int
    config: RouteMonitorConfig
    """Advisory production monitor intent, independent of the canary guard."""
    project_id: UUID | Unset = UNSET
    environment_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        scope = self.scope

        budget_source: str = self.budget_source

        revision = self.revision

        app_revision = self.app_revision

        config = self.config.to_dict()

        project_id: str | Unset = UNSET
        if not isinstance(self.project_id, Unset):
            project_id = str(self.project_id)

        environment_id: str | Unset = UNSET
        if not isinstance(self.environment_id, Unset):
            environment_id = str(self.environment_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "scope": scope,
                "budget_source": budget_source,
                "revision": revision,
                "app_revision": app_revision,
                "config": config,
            }
        )
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_config import RouteMonitorConfig

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        budget_source = check_environment_route_budgets_budget_source(d.pop("budget_source"))

        revision = d.pop("revision")

        app_revision = d.pop("app_revision")

        config = RouteMonitorConfig.from_dict(d.pop("config"))

        _project_id = d.pop("project_id", UNSET)
        project_id: UUID | Unset
        if isinstance(_project_id, Unset):
            project_id = UNSET
        else:
            project_id = UUID(_project_id)

        _environment_id = d.pop("environment_id", UNSET)
        environment_id: UUID | Unset
        if isinstance(_environment_id, Unset):
            environment_id = UNSET
        else:
            environment_id = UUID(_environment_id)

        environment_route_budgets = cls(
            app_id=app_id,
            scope=scope,
            budget_source=budget_source,
            revision=revision,
            app_revision=app_revision,
            config=config,
            project_id=project_id,
            environment_id=environment_id,
        )

        environment_route_budgets.additional_properties = d
        return environment_route_budgets

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
