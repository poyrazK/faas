from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.project_environment_edge_rule_response import ProjectEnvironmentEdgeRuleResponse


T = TypeVar("T", bound="UpdateProjectEnvironmentRoutingPolicyRequest")


@_attrs_define
class UpdateProjectEnvironmentRoutingPolicyRequest:
    """Complete replacement for environment redirect/rewrite rules, independently of headers/CORS rules."""

    rules: list[ProjectEnvironmentEdgeRuleResponse]

    def to_dict(self) -> dict[str, Any]:
        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "rules": rules,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_edge_rule_response import ProjectEnvironmentEdgeRuleResponse

        d = dict(src_dict)
        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = ProjectEnvironmentEdgeRuleResponse.from_dict(rules_item_data)

            rules.append(rules_item)

        update_project_environment_routing_policy_request = cls(
            rules=rules,
        )

        return update_project_environment_routing_policy_request
