from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.update_edge_rule_request_mode import UpdateEdgeRuleRequestMode, check_update_edge_rule_request_mode
from ..models.update_edge_rule_request_validate_mode import (
    UpdateEdgeRuleRequestValidateMode,
    check_update_edge_rule_request_validate_mode,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.edge_rule_async_action import EdgeRuleAsyncAction
    from ..models.edge_rule_budget_action import EdgeRuleBudgetAction
    from ..models.edge_rule_cache_action import EdgeRuleCacheAction
    from ..models.edge_rule_challenge_action import EdgeRuleChallengeAction
    from ..models.edge_rule_circuit_breaker_action import EdgeRuleCircuitBreakerAction
    from ..models.edge_rule_cors_action import EdgeRuleCORSAction
    from ..models.edge_rule_geo_action import EdgeRuleGeoAction
    from ..models.edge_rule_headers_action import EdgeRuleHeadersAction
    from ..models.edge_rule_ip_action import EdgeRuleIPAction
    from ..models.edge_rule_jwt_action import EdgeRuleJWTAction
    from ..models.edge_rule_limit_action import EdgeRuleLimitAction
    from ..models.edge_rule_maintenance_action import EdgeRuleMaintenanceAction
    from ..models.edge_rule_match_expr import EdgeRuleMatchExpr
    from ..models.edge_rule_penalty import EdgeRulePenalty
    from ..models.edge_rule_redirect_action import EdgeRuleRedirectAction
    from ..models.edge_rule_respond_action import EdgeRuleRespondAction
    from ..models.edge_rule_retry_action import EdgeRuleRetryAction
    from ..models.edge_rule_rewrite_action import EdgeRuleRewriteAction
    from ..models.edge_rule_route_action import EdgeRuleRouteAction
    from ..models.edge_rule_throttle_action import EdgeRuleThrottleAction
    from ..models.edge_rule_validate_action import EdgeRuleValidateAction
    from ..models.edge_rule_waf_action import EdgeRuleWAFAction
    from ..models.update_edge_rule_request_match_headers import UpdateEdgeRuleRequestMatchHeaders


T = TypeVar("T", bound="UpdateEdgeRuleRequest")


@_attrs_define
class UpdateEdgeRuleRequest:
    """Partial update — every field optional. Kind is not patchable."""

    match_host: str | Unset = UNSET
    match_path: str | Unset = UNSET
    match_methods: list[str] | Unset = UNSET
    match_headers: UpdateEdgeRuleRequestMatchHeaders | Unset = UNSET
    """Replacing this map changes the request-header selectors. Header names compare case-insensitively and values
    compare exactly; use an empty object to remove all header conditions."""
    priority: int | Unset = UNSET
    enabled: bool | Unset = UNSET
    validate_mode: UpdateEdgeRuleRequestValidateMode | Unset = UNSET
    """Top-level source of truth for kind=validate.
    Omit (do not send) to leave the column untouched.
    """
    action: (
        EdgeRuleAsyncAction
        | EdgeRuleBudgetAction
        | EdgeRuleCacheAction
        | EdgeRuleChallengeAction
        | EdgeRuleCircuitBreakerAction
        | EdgeRuleCORSAction
        | EdgeRuleGeoAction
        | EdgeRuleHeadersAction
        | EdgeRuleIPAction
        | EdgeRuleJWTAction
        | EdgeRuleLimitAction
        | EdgeRuleMaintenanceAction
        | EdgeRuleRedirectAction
        | EdgeRuleRespondAction
        | EdgeRuleRetryAction
        | EdgeRuleRewriteAction
        | EdgeRuleRouteAction
        | EdgeRuleThrottleAction
        | EdgeRuleValidateAction
        | EdgeRuleWAFAction
        | Unset
    ) = UNSET
    """Replaces the jsonb column whole."""
    name: str | Unset = UNSET
    """Replacement rule name."""
    description: str | Unset = UNSET
    """Replacement rule notes."""
    expires_at: datetime.datetime | Unset = UNSET
    """New expiry; the gateway stops applying the rule after it."""
    clear_expires_at: bool | Unset = UNSET
    """Remove the expiry so the rule applies indefinitely."""
    match: EdgeRuleMatchExpr | Unset = UNSET
    """Match condition, ANDed with the rule's fixed selectors. A node
    is exactly one of all, any, not, or a field/op leaf. Fields: method,
    path, host, client_ip, country, asn (the client IP's
    autonomous system, e.g. 13335 or AS13335), ua_family (the
    User-Agent's family, one of browser, mobile, bot, tool, other),
    verified_bot (a known crawler confirmed by its operator's
    published IP ranges or forward-confirmed reverse DNS of the client
    IP, one of googlebot, bingbot, applebot, yandexbot, baiduspider,
    yahoo, petalbot; absent otherwise), ai_crawler (the AI crawler the User-Agent claims, such as gptbot, claudebot,
    ccbot, perplexitybot, bytespider), ai_crawler_purpose (that
    crawler's purpose: training, search or assistant),
    verified_ai_crawler (the claimed AI crawler when the client IP is
    in its operator's published ranges; only crawlers that publish
    ranges can verify), header:<name>, cookie:<name>, query:<name>.
    ua_family, verified_bot and the AI crawler fields take eq, ne, in,
    not_in, exists, missing. Ops: eq, ne, in, not_in, prefix, suffix, contains,
    exists, missing, regex (RE2), cidr (client_ip only), in_list (list names an account list whose kind fits the
    field). Depth at most 4,
    at most 32 nodes, 64 values per leaf, values and regexes at most 256
    bytes. An untrusted client IP, or its unknown country or ASN, is absent.
    """
    clear_match: bool | Unset = UNSET
    """Remove the match condition."""
    mode: UpdateEdgeRuleRequestMode | Unset = UNSET
    """Switch the rule to enforce, or to log so it is matched and counted but never acts."""
    penalty: EdgeRulePenalty | Unset = UNSET
    """A rule's penalty box. Once the rule has refused
    the same client `after` times within `within_seconds`, every request
    from that client to the app is refused with 403 `penalty_box` and
    Retry-After for `seconds`, whichever rule or path it targets. The
    client is its IP address (an IPv6 client is its /64). Boxes are held
    in each gateway node's memory. Allowed on ip, geo, throttle and waf
    (block mode) rules; editing the rule's penalty away, disabling it or
    putting it in log mode releases its clients.
    """
    clear_penalty: bool | Unset = UNSET
    """Remove the rule's penalty box, releasing the clients it holds."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.edge_rule_async_action import EdgeRuleAsyncAction
        from ..models.edge_rule_budget_action import EdgeRuleBudgetAction
        from ..models.edge_rule_cache_action import EdgeRuleCacheAction
        from ..models.edge_rule_circuit_breaker_action import EdgeRuleCircuitBreakerAction
        from ..models.edge_rule_cors_action import EdgeRuleCORSAction
        from ..models.edge_rule_geo_action import EdgeRuleGeoAction
        from ..models.edge_rule_headers_action import EdgeRuleHeadersAction
        from ..models.edge_rule_ip_action import EdgeRuleIPAction
        from ..models.edge_rule_jwt_action import EdgeRuleJWTAction
        from ..models.edge_rule_limit_action import EdgeRuleLimitAction
        from ..models.edge_rule_maintenance_action import EdgeRuleMaintenanceAction
        from ..models.edge_rule_redirect_action import EdgeRuleRedirectAction
        from ..models.edge_rule_respond_action import EdgeRuleRespondAction
        from ..models.edge_rule_retry_action import EdgeRuleRetryAction
        from ..models.edge_rule_rewrite_action import EdgeRuleRewriteAction
        from ..models.edge_rule_route_action import EdgeRuleRouteAction
        from ..models.edge_rule_throttle_action import EdgeRuleThrottleAction
        from ..models.edge_rule_validate_action import EdgeRuleValidateAction
        from ..models.edge_rule_waf_action import EdgeRuleWAFAction

        match_host = self.match_host

        match_path = self.match_path

        match_methods: list[str] | Unset = UNSET
        if not isinstance(self.match_methods, Unset):
            match_methods = self.match_methods

        match_headers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.match_headers, Unset):
            match_headers = self.match_headers.to_dict()

        priority = self.priority

        enabled = self.enabled

        validate_mode: str | Unset = UNSET
        if not isinstance(self.validate_mode, Unset):
            validate_mode = self.validate_mode

        action: dict[str, Any] | Unset
        if isinstance(self.action, Unset):
            action = UNSET
        elif isinstance(self.action, EdgeRuleRouteAction):
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
        elif isinstance(self.action, EdgeRuleCacheAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleRespondAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleRetryAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleCircuitBreakerAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleAsyncAction):
            action = self.action.to_dict()
        elif isinstance(self.action, EdgeRuleWAFAction):
            action = self.action.to_dict()
        else:
            action = self.action.to_dict()

        name = self.name

        description = self.description

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        clear_expires_at = self.clear_expires_at

        match: dict[str, Any] | Unset = UNSET
        if not isinstance(self.match, Unset):
            match = self.match.to_dict()

        clear_match = self.clear_match

        mode: str | Unset = UNSET
        if not isinstance(self.mode, Unset):
            mode = self.mode

        penalty: dict[str, Any] | Unset = UNSET
        if not isinstance(self.penalty, Unset):
            penalty = self.penalty.to_dict()

        clear_penalty = self.clear_penalty

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if match_host is not UNSET:
            field_dict["match_host"] = match_host
        if match_path is not UNSET:
            field_dict["match_path"] = match_path
        if match_methods is not UNSET:
            field_dict["match_methods"] = match_methods
        if match_headers is not UNSET:
            field_dict["match_headers"] = match_headers
        if priority is not UNSET:
            field_dict["priority"] = priority
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if validate_mode is not UNSET:
            field_dict["validate_mode"] = validate_mode
        if action is not UNSET:
            field_dict["action"] = action
        if name is not UNSET:
            field_dict["name"] = name
        if description is not UNSET:
            field_dict["description"] = description
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if clear_expires_at is not UNSET:
            field_dict["clear_expires_at"] = clear_expires_at
        if match is not UNSET:
            field_dict["match"] = match
        if clear_match is not UNSET:
            field_dict["clear_match"] = clear_match
        if mode is not UNSET:
            field_dict["mode"] = mode
        if penalty is not UNSET:
            field_dict["penalty"] = penalty
        if clear_penalty is not UNSET:
            field_dict["clear_penalty"] = clear_penalty

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_async_action import EdgeRuleAsyncAction
        from ..models.edge_rule_budget_action import EdgeRuleBudgetAction
        from ..models.edge_rule_cache_action import EdgeRuleCacheAction
        from ..models.edge_rule_challenge_action import EdgeRuleChallengeAction
        from ..models.edge_rule_circuit_breaker_action import EdgeRuleCircuitBreakerAction
        from ..models.edge_rule_cors_action import EdgeRuleCORSAction
        from ..models.edge_rule_geo_action import EdgeRuleGeoAction
        from ..models.edge_rule_headers_action import EdgeRuleHeadersAction
        from ..models.edge_rule_ip_action import EdgeRuleIPAction
        from ..models.edge_rule_jwt_action import EdgeRuleJWTAction
        from ..models.edge_rule_limit_action import EdgeRuleLimitAction
        from ..models.edge_rule_maintenance_action import EdgeRuleMaintenanceAction
        from ..models.edge_rule_match_expr import EdgeRuleMatchExpr
        from ..models.edge_rule_penalty import EdgeRulePenalty
        from ..models.edge_rule_redirect_action import EdgeRuleRedirectAction
        from ..models.edge_rule_respond_action import EdgeRuleRespondAction
        from ..models.edge_rule_retry_action import EdgeRuleRetryAction
        from ..models.edge_rule_rewrite_action import EdgeRuleRewriteAction
        from ..models.edge_rule_route_action import EdgeRuleRouteAction
        from ..models.edge_rule_throttle_action import EdgeRuleThrottleAction
        from ..models.edge_rule_validate_action import EdgeRuleValidateAction
        from ..models.edge_rule_waf_action import EdgeRuleWAFAction
        from ..models.update_edge_rule_request_match_headers import UpdateEdgeRuleRequestMatchHeaders

        d = dict(src_dict)
        match_host = d.pop("match_host", UNSET)

        match_path = d.pop("match_path", UNSET)

        match_methods = cast(list[str], d.pop("match_methods", UNSET))

        _match_headers = d.pop("match_headers", UNSET)
        match_headers: UpdateEdgeRuleRequestMatchHeaders | Unset
        if isinstance(_match_headers, Unset):
            match_headers = UNSET
        else:
            match_headers = UpdateEdgeRuleRequestMatchHeaders.from_dict(_match_headers)

        priority = d.pop("priority", UNSET)

        enabled = d.pop("enabled", UNSET)

        _validate_mode = d.pop("validate_mode", UNSET)
        validate_mode: UpdateEdgeRuleRequestValidateMode | Unset
        if isinstance(_validate_mode, Unset):
            validate_mode = UNSET
        else:
            validate_mode = check_update_edge_rule_request_validate_mode(_validate_mode)

        def _parse_action(
            data: object,
        ) -> (
            EdgeRuleAsyncAction
            | EdgeRuleBudgetAction
            | EdgeRuleCacheAction
            | EdgeRuleChallengeAction
            | EdgeRuleCircuitBreakerAction
            | EdgeRuleCORSAction
            | EdgeRuleGeoAction
            | EdgeRuleHeadersAction
            | EdgeRuleIPAction
            | EdgeRuleJWTAction
            | EdgeRuleLimitAction
            | EdgeRuleMaintenanceAction
            | EdgeRuleRedirectAction
            | EdgeRuleRespondAction
            | EdgeRuleRetryAction
            | EdgeRuleRewriteAction
            | EdgeRuleRouteAction
            | EdgeRuleThrottleAction
            | EdgeRuleValidateAction
            | EdgeRuleWAFAction
            | Unset
        ):
            if isinstance(data, Unset):
                return data
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
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_13 = EdgeRuleCacheAction.from_dict(data)

                return action_type_13
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_14 = EdgeRuleRespondAction.from_dict(data)

                return action_type_14
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_15 = EdgeRuleRetryAction.from_dict(data)

                return action_type_15
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_16 = EdgeRuleCircuitBreakerAction.from_dict(data)

                return action_type_16
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_17 = EdgeRuleAsyncAction.from_dict(data)

                return action_type_17
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                action_type_18 = EdgeRuleWAFAction.from_dict(data)

                return action_type_18
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            if not isinstance(data, dict):
                raise TypeError()
            action_type_19 = EdgeRuleChallengeAction.from_dict(data)

            return action_type_19

        action = _parse_action(d.pop("action", UNSET))

        name = d.pop("name", UNSET)

        description = d.pop("description", UNSET)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        clear_expires_at = d.pop("clear_expires_at", UNSET)

        _match = d.pop("match", UNSET)
        match: EdgeRuleMatchExpr | Unset
        if isinstance(_match, Unset):
            match = UNSET
        else:
            match = EdgeRuleMatchExpr.from_dict(_match)

        clear_match = d.pop("clear_match", UNSET)

        _mode = d.pop("mode", UNSET)
        mode: UpdateEdgeRuleRequestMode | Unset
        if isinstance(_mode, Unset):
            mode = UNSET
        else:
            mode = check_update_edge_rule_request_mode(_mode)

        _penalty = d.pop("penalty", UNSET)
        penalty: EdgeRulePenalty | Unset
        if isinstance(_penalty, Unset):
            penalty = UNSET
        else:
            penalty = EdgeRulePenalty.from_dict(_penalty)

        clear_penalty = d.pop("clear_penalty", UNSET)

        update_edge_rule_request = cls(
            match_host=match_host,
            match_path=match_path,
            match_methods=match_methods,
            match_headers=match_headers,
            priority=priority,
            enabled=enabled,
            validate_mode=validate_mode,
            action=action,
            name=name,
            description=description,
            expires_at=expires_at,
            clear_expires_at=clear_expires_at,
            match=match,
            clear_match=clear_match,
            mode=mode,
            penalty=penalty,
            clear_penalty=clear_penalty,
        )

        update_edge_rule_request.additional_properties = d
        return update_edge_rule_request

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
