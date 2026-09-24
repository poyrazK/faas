from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.project_environment_edge_rule_response_kind import (
    ProjectEnvironmentEdgeRuleResponseKind,
    check_project_environment_edge_rule_response_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_environment_edge_rule_response_action import ProjectEnvironmentEdgeRuleResponseAction
    from ..models.project_environment_edge_rule_response_match_headers import (
        ProjectEnvironmentEdgeRuleResponseMatchHeaders,
    )


T = TypeVar("T", bound="ProjectEnvironmentEdgeRuleResponse")


@_attrs_define
class ProjectEnvironmentEdgeRuleResponse:
    """One environment-owned edge rule on a stable environment URL."""

    kind: ProjectEnvironmentEdgeRuleResponseKind
    match_path: str
    priority: int
    enabled: bool
    action: ProjectEnvironmentEdgeRuleResponseAction
    match_methods: list[str] | Unset = UNSET
    match_headers: ProjectEnvironmentEdgeRuleResponseMatchHeaders | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        match_path = self.match_path

        priority = self.priority

        enabled = self.enabled

        action = self.action.to_dict()

        match_methods: list[str] | Unset = UNSET
        if not isinstance(self.match_methods, Unset):
            match_methods = self.match_methods

        match_headers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.match_headers, Unset):
            match_headers = self.match_headers.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "match_path": match_path,
                "priority": priority,
                "enabled": enabled,
                "action": action,
            }
        )
        if match_methods is not UNSET:
            field_dict["match_methods"] = match_methods
        if match_headers is not UNSET:
            field_dict["match_headers"] = match_headers

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_edge_rule_response_action import ProjectEnvironmentEdgeRuleResponseAction
        from ..models.project_environment_edge_rule_response_match_headers import (
            ProjectEnvironmentEdgeRuleResponseMatchHeaders,
        )

        d = dict(src_dict)
        kind = check_project_environment_edge_rule_response_kind(d.pop("kind"))

        match_path = d.pop("match_path")

        priority = d.pop("priority")

        enabled = d.pop("enabled")

        action = ProjectEnvironmentEdgeRuleResponseAction.from_dict(d.pop("action"))

        match_methods = cast(list[str], d.pop("match_methods", UNSET))

        _match_headers = d.pop("match_headers", UNSET)
        match_headers: ProjectEnvironmentEdgeRuleResponseMatchHeaders | Unset
        if isinstance(_match_headers, Unset):
            match_headers = UNSET
        else:
            match_headers = ProjectEnvironmentEdgeRuleResponseMatchHeaders.from_dict(_match_headers)

        project_environment_edge_rule_response = cls(
            kind=kind,
            match_path=match_path,
            priority=priority,
            enabled=enabled,
            action=action,
            match_methods=match_methods,
            match_headers=match_headers,
        )

        return project_environment_edge_rule_response
