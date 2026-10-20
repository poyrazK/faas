from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.environment_policy_kind import EnvironmentPolicyKind, check_environment_policy_kind
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.edge_rule_cors_action import EdgeRuleCORSAction
    from ..models.edge_rule_headers_action import EdgeRuleHeadersAction
    from ..models.environment_policy_match_headers import EnvironmentPolicyMatchHeaders


T = TypeVar("T", bound="EnvironmentPolicy")


@_attrs_define
class EnvironmentPolicy:
    """Named environment-scoped headers or CORS rule."""

    name: str
    kind: EnvironmentPolicyKind
    match_path: str
    priority: int
    action: EdgeRuleCORSAction | EdgeRuleHeadersAction
    match_methods: list[str] | Unset = UNSET
    match_headers: EnvironmentPolicyMatchHeaders | Unset = UNSET
    enabled: bool | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        from ..models.edge_rule_headers_action import EdgeRuleHeadersAction

        name = self.name

        kind: str = self.kind

        match_path = self.match_path

        priority = self.priority

        action: dict[str, Any]
        if isinstance(self.action, EdgeRuleHeadersAction):
            action = self.action.to_dict()
        else:
            action = self.action.to_dict()

        match_methods: list[str] | Unset = UNSET
        if not isinstance(self.match_methods, Unset):
            match_methods = self.match_methods

        match_headers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.match_headers, Unset):
            match_headers = self.match_headers.to_dict()

        enabled = self.enabled

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "kind": kind,
                "match_path": match_path,
                "priority": priority,
                "action": action,
            }
        )
        if match_methods is not UNSET:
            field_dict["match_methods"] = match_methods
        if match_headers is not UNSET:
            field_dict["match_headers"] = match_headers
        if enabled is not UNSET:
            field_dict["enabled"] = enabled

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_cors_action import EdgeRuleCORSAction
        from ..models.edge_rule_headers_action import EdgeRuleHeadersAction
        from ..models.environment_policy_match_headers import EnvironmentPolicyMatchHeaders

        d = dict(src_dict)
        name = d.pop("name")

        kind = check_environment_policy_kind(d.pop("kind"))

        match_path = d.pop("match_path")

        priority = d.pop("priority")

        def _parse_action(data: object) -> EdgeRuleCORSAction | EdgeRuleHeadersAction:
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_0 = EdgeRuleHeadersAction.from_dict(data)

                return action_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            if not isinstance(data, dict):
                raise TypeError()
            action_type_1 = EdgeRuleCORSAction.from_dict(data)

            return action_type_1

        action = _parse_action(d.pop("action"))

        match_methods = cast(list[str], d.pop("match_methods", UNSET))

        _match_headers = d.pop("match_headers", UNSET)
        match_headers: EnvironmentPolicyMatchHeaders | Unset
        if isinstance(_match_headers, Unset):
            match_headers = UNSET
        else:
            match_headers = EnvironmentPolicyMatchHeaders.from_dict(_match_headers)

        enabled = d.pop("enabled", UNSET)

        environment_policy = cls(
            name=name,
            kind=kind,
            match_path=match_path,
            priority=priority,
            action=action,
            match_methods=match_methods,
            match_headers=match_headers,
            enabled=enabled,
        )

        return environment_policy
