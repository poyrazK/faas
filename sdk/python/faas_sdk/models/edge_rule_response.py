from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.edge_rule_response_kind import EdgeRuleResponseKind, check_edge_rule_response_kind
from ..models.edge_rule_response_validate_mode import (
    EdgeRuleResponseValidateMode,
    check_edge_rule_response_validate_mode,
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


T = TypeVar("T", bound="EdgeRuleResponse")


@_attrs_define
class EdgeRuleResponse:
    """A customer-configurable edge rule. The `action` blob is a
    kind-tagged union — the shape varies by `kind`. See
    CreateEdgeRuleRequest.action for the per-kind shape.

    """

    id: str
    account_id: str
    app_id: str
    match_host: str
    """Host glob. `*` matches any; `*.example.com` matches any subdomain."""
    match_path: str
    """Path glob. Trailing `*` matches anything beneath."""
    match_methods: list[str]
    """Empty array = match any method."""
    kind: EdgeRuleResponseKind
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
    """Kind-tagged union — shape varies by `kind`."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    priority: int = 100
    enabled: bool = True
    validate_mode: EdgeRuleResponseValidateMode | Unset = "block"
    """Top-level source of truth for kind=validate (ADR-128).
    Resolved mode; always present on read. Empty on read
    would be a database invariant violation.
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

        id = self.id

        account_id = self.account_id

        app_id = self.app_id

        match_host = self.match_host

        match_path = self.match_path

        match_methods = self.match_methods

        priority = self.priority

        enabled = self.enabled

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

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        validate_mode: str | Unset = UNSET
        if not isinstance(self.validate_mode, Unset):
            validate_mode = self.validate_mode

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "account_id": account_id,
                "app_id": app_id,
                "match_host": match_host,
                "match_path": match_path,
                "match_methods": match_methods,
                "priority": priority,
                "enabled": enabled,
                "kind": kind,
                "action": action,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
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
        id = d.pop("id")

        account_id = d.pop("account_id")

        app_id = d.pop("app_id")

        match_host = d.pop("match_host")

        match_path = d.pop("match_path")

        match_methods = cast(list[str], d.pop("match_methods"))

        priority = d.pop("priority")

        enabled = d.pop("enabled")

        kind = check_edge_rule_response_kind(d.pop("kind"))

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

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _validate_mode = d.pop("validate_mode", UNSET)
        validate_mode: EdgeRuleResponseValidateMode | Unset
        if isinstance(_validate_mode, Unset):
            validate_mode = UNSET
        else:
            validate_mode = check_edge_rule_response_validate_mode(_validate_mode)

        edge_rule_response = cls(
            id=id,
            account_id=account_id,
            app_id=app_id,
            match_host=match_host,
            match_path=match_path,
            match_methods=match_methods,
            priority=priority,
            enabled=enabled,
            kind=kind,
            action=action,
            created_at=created_at,
            updated_at=updated_at,
            validate_mode=validate_mode,
        )

        edge_rule_response.additional_properties = d
        return edge_rule_response

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
