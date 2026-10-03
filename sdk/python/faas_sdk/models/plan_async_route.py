from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.plan_async_route_action import PlanAsyncRouteAction, check_plan_async_route_action
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.retry_policy_dto import RetryPolicyDTO


T = TypeVar("T", bound="PlanAsyncRoute")


@_attrs_define
class PlanAsyncRoute:
    """One manifest-owned async-route reconciliation row in a project deployment plan. Route fields describe the planned
    route, or the existing route for a removal.

    """

    app: str
    """Workload or app slug that owns the route"""
    name: str
    """Stable async-route identity within the app"""
    action: PlanAsyncRouteAction
    """Reconciliation result; skipped means selection flags or --no-triggers leave this route untouched."""
    match_host: str
    match_path: str
    match_methods: list[str]
    priority: int
    enabled: bool
    on_success: str | Unset = UNSET
    """App webhook destination for successful executions"""
    on_failure: str | Unset = UNSET
    """App webhook destination for failed executions"""
    retry_policy: RetryPolicyDTO | Unset = UNSET
    """ADR-134 PR-B. Wire shape for dispatch.RetryPolicy. max_attempts
    is a requested total-attempt count; zero inherits the applicable
    account plan and never means unlimited. Durable invocation
    producers materialize the effective plan-capped value, and the
    scheduler re-clamps it at dispatch time to account for later plan
    downgrades. Lives in pkg/api so the SDK can type the policy
    without importing pkg/dispatch directly.
    """
    max_age_seconds: int | Unset = UNSET
    reason: str | Unset = UNSET
    """Why this route is skipped, or what reconciliation does"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        name = self.name

        action: str = self.action

        match_host = self.match_host

        match_path = self.match_path

        match_methods = self.match_methods

        priority = self.priority

        enabled = self.enabled

        on_success = self.on_success

        on_failure = self.on_failure

        retry_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retry_policy, Unset):
            retry_policy = self.retry_policy.to_dict()

        max_age_seconds = self.max_age_seconds

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "name": name,
                "action": action,
                "match_host": match_host,
                "match_path": match_path,
                "match_methods": match_methods,
                "priority": priority,
                "enabled": enabled,
            }
        )
        if on_success is not UNSET:
            field_dict["on_success"] = on_success
        if on_failure is not UNSET:
            field_dict["on_failure"] = on_failure
        if retry_policy is not UNSET:
            field_dict["retry_policy"] = retry_policy
        if max_age_seconds is not UNSET:
            field_dict["max_age_seconds"] = max_age_seconds
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.retry_policy_dto import RetryPolicyDTO

        d = dict(src_dict)
        app = d.pop("app")

        name = d.pop("name")

        action = check_plan_async_route_action(d.pop("action"))

        match_host = d.pop("match_host")

        match_path = d.pop("match_path")

        match_methods = cast(list[str], d.pop("match_methods"))

        priority = d.pop("priority")

        enabled = d.pop("enabled")

        on_success = d.pop("on_success", UNSET)

        on_failure = d.pop("on_failure", UNSET)

        _retry_policy = d.pop("retry_policy", UNSET)
        retry_policy: RetryPolicyDTO | Unset
        if isinstance(_retry_policy, Unset):
            retry_policy = UNSET
        else:
            retry_policy = RetryPolicyDTO.from_dict(_retry_policy)

        max_age_seconds = d.pop("max_age_seconds", UNSET)

        reason = d.pop("reason", UNSET)

        plan_async_route = cls(
            app=app,
            name=name,
            action=action,
            match_host=match_host,
            match_path=match_path,
            match_methods=match_methods,
            priority=priority,
            enabled=enabled,
            on_success=on_success,
            on_failure=on_failure,
            retry_policy=retry_policy,
            max_age_seconds=max_age_seconds,
            reason=reason,
        )

        plan_async_route.additional_properties = d
        return plan_async_route

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
