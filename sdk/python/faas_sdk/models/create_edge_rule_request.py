from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_edge_rule_request_kind import CreateEdgeRuleRequestKind, check_create_edge_rule_request_kind
from ..models.create_edge_rule_request_validate_mode import (
    CreateEdgeRuleRequestValidateMode,
    check_create_edge_rule_request_validate_mode,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.edge_rule_budget_action import EdgeRuleBudgetAction
    from ..models.edge_rule_cors_action import EdgeRuleCORSAction
    from ..models.edge_rule_geo_action import EdgeRuleGeoAction
    from ..models.edge_rule_headers_action import EdgeRuleHeadersAction
    from ..models.edge_rule_ip_action import EdgeRuleIPAction
    from ..models.edge_rule_jwt_action import EdgeRuleJWTAction
    from ..models.edge_rule_limit_action import EdgeRuleLimitAction
    from ..models.edge_rule_maintenance_action import EdgeRuleMaintenanceAction
    from ..models.edge_rule_redirect_action import EdgeRuleRedirectAction
    from ..models.edge_rule_respond_action import EdgeRuleRespondAction
    from ..models.edge_rule_rewrite_action import EdgeRuleRewriteAction
    from ..models.edge_rule_route_action import EdgeRuleRouteAction
    from ..models.edge_rule_throttle_action import EdgeRuleThrottleAction
    from ..models.edge_rule_validate_action import EdgeRuleValidateAction


T = TypeVar("T", bound="CreateEdgeRuleRequest")


@_attrs_define
class CreateEdgeRuleRequest:
    """Body shape for POST /v1/apps/{slug}/edge-rules."""

    match_host: str
    kind: CreateEdgeRuleRequestKind
    action: (
        EdgeRuleBudgetAction
        | EdgeRuleCORSAction
        | EdgeRuleGeoAction
        | EdgeRuleHeadersAction
        | EdgeRuleIPAction
        | EdgeRuleJWTAction
        | EdgeRuleLimitAction
        | EdgeRuleMaintenanceAction
        | EdgeRuleRedirectAction
        | EdgeRuleRespondAction
        | EdgeRuleRewriteAction
        | EdgeRuleRouteAction
        | EdgeRuleThrottleAction
        | EdgeRuleValidateAction
    )
    """Kind-tagged action body — shape depends on `kind`."""
    match_path: str | Unset = "/"
    match_methods: list[str] | Unset = UNSET
    priority: int | Unset = 100
    enabled: bool | Unset = True
    validate_mode: CreateEdgeRuleRequestValidateMode | Unset = "block"
    """Top-level source of truth for kind=validate (ADR-128).
    Omitted == 'block' (the SQL-side default).
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.edge_rule_budget_action import EdgeRuleBudgetAction
        from ..models.edge_rule_cors_action import EdgeRuleCORSAction
        from ..models.edge_rule_geo_action import EdgeRuleGeoAction
        from ..models.edge_rule_headers_action import EdgeRuleHeadersAction
        from ..models.edge_rule_ip_action import EdgeRuleIPAction
        from ..models.edge_rule_jwt_action import EdgeRuleJWTAction
        from ..models.edge_rule_limit_action import EdgeRuleLimitAction
        from ..models.edge_rule_maintenance_action import EdgeRuleMaintenanceAction
        from ..models.edge_rule_redirect_action import EdgeRuleRedirectAction
        from ..models.edge_rule_rewrite_action import EdgeRuleRewriteAction
        from ..models.edge_rule_route_action import EdgeRuleRouteAction
        from ..models.edge_rule_throttle_action import EdgeRuleThrottleAction
        from ..models.edge_rule_validate_action import EdgeRuleValidateAction

        match_host = self.match_host

        kind: str = self.kind

        action: dict[str, Any]
        if isinstance(self.action, EdgeRuleRouteAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleRewriteAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleRedirectAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleHeadersAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleCORSAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleJWTAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleIPAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleValidateAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleLimitAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleMaintenanceAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleGeoAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleThrottleAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleBudgetAction):
            action = self.action.to_dict()
        else:
            action = self.action.to_dict()

        match_path = self.match_path

        match_methods: list[str] | Unset = UNSET
        if not isinstance(self.match_methods, Unset):
            match_methods = self.match_methods

        priority = self.priority

        enabled = self.enabled

        validate_mode: str | Unset = UNSET
        if not isinstance(self.validate_mode, Unset):
            validate_mode = self.validate_mode

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "match_host": match_host,
                "kind": kind,
                "action": action,
            }
        )
        if match_path is not UNSET:
            field_dict["match_path"] = match_path
        if match_methods is not UNSET:
            field_dict["match_methods"] = match_methods
        if priority is not UNSET:
            field_dict["priority"] = priority
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if validate_mode is not UNSET:
            field_dict["validate_mode"] = validate_mode

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_budget_action import EdgeRuleBudgetAction
        from ..models.edge_rule_cors_action import EdgeRuleCORSAction
        from ..models.edge_rule_geo_action import EdgeRuleGeoAction
        from ..models.edge_rule_headers_action import EdgeRuleHeadersAction
        from ..models.edge_rule_ip_action import EdgeRuleIPAction
        from ..models.edge_rule_jwt_action import EdgeRuleJWTAction
        from ..models.edge_rule_limit_action import EdgeRuleLimitAction
        from ..models.edge_rule_maintenance_action import EdgeRuleMaintenanceAction
        from ..models.edge_rule_redirect_action import EdgeRuleRedirectAction
        from ..models.edge_rule_respond_action import EdgeRuleRespondAction
        from ..models.edge_rule_rewrite_action import EdgeRuleRewriteAction
        from ..models.edge_rule_route_action import EdgeRuleRouteAction
        from ..models.edge_rule_throttle_action import EdgeRuleThrottleAction
        from ..models.edge_rule_validate_action import EdgeRuleValidateAction

        d = dict(src_dict)
        match_host = d.pop("match_host")

        kind = check_create_edge_rule_request_kind(d.pop("kind"))

        def _parse_action(
            data: object,
        ) -> (
            EdgeRuleBudgetAction
            | EdgeRuleCORSAction
            | EdgeRuleGeoAction
            | EdgeRuleHeadersAction
            | EdgeRuleIPAction
            | EdgeRuleJWTAction
            | EdgeRuleLimitAction
            | EdgeRuleMaintenanceAction
            | EdgeRuleRedirectAction
            | EdgeRuleRespondAction
            | EdgeRuleRewriteAction
            | EdgeRuleRouteAction
            | EdgeRuleThrottleAction
            | EdgeRuleValidateAction
        ):
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_0 = EdgeRuleRouteAction.from_dict(data)

                return action_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_1 = EdgeRuleRewriteAction.from_dict(data)

                return action_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_2 = EdgeRuleRedirectAction.from_dict(data)

                return action_type_2
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_3 = EdgeRuleHeadersAction.from_dict(data)

                return action_type_3
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_4 = EdgeRuleCORSAction.from_dict(data)

                return action_type_4
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_5 = EdgeRuleJWTAction.from_dict(data)

                return action_type_5
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_6 = EdgeRuleIPAction.from_dict(data)

                return action_type_6
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_7 = EdgeRuleValidateAction.from_dict(data)

                return action_type_7
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_8 = EdgeRuleLimitAction.from_dict(data)

                return action_type_8
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_9 = EdgeRuleMaintenanceAction.from_dict(data)

                return action_type_9
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_10 = EdgeRuleGeoAction.from_dict(data)

                return action_type_10
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_11 = EdgeRuleThrottleAction.from_dict(data)

                return action_type_11
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_12 = EdgeRuleBudgetAction.from_dict(data)

                return action_type_12
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            if not isinstance(data, dict):
                raise TypeError()
            action_type_13 = EdgeRuleRespondAction.from_dict(data)

            return action_type_13

        action = _parse_action(d.pop("action"))

        match_path = d.pop("match_path", UNSET)

        match_methods = cast(list[str], d.pop("match_methods", UNSET))

        priority = d.pop("priority", UNSET)

        enabled = d.pop("enabled", UNSET)

        _validate_mode = d.pop("validate_mode", UNSET)
        validate_mode: CreateEdgeRuleRequestValidateMode | Unset
        if isinstance(_validate_mode, Unset):
            validate_mode = UNSET
        else:
            validate_mode = check_create_edge_rule_request_validate_mode(_validate_mode)

        create_edge_rule_request = cls(
            match_host=match_host,
            kind=kind,
            action=action,
            match_path=match_path,
            match_methods=match_methods,
            priority=priority,
            enabled=enabled,
            validate_mode=validate_mode,
        )

        create_edge_rule_request.additional_properties = d
        return create_edge_rule_request

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
