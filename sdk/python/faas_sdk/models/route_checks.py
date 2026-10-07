from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.route_checks_authentication import RouteChecksAuthentication, check_route_checks_authentication
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_budget_requirement import RouteBudgetRequirement
    from ..models.route_throttle_requirement import RouteThrottleRequirement


T = TypeVar("T", bound="RouteChecks")


@_attrs_define
class RouteChecks:
    """At least one policy requirement; application authorization remains outside configuration verification."""

    authentication: RouteChecksAuthentication | Unset = UNSET
    throttle: RouteThrottleRequirement | Unset = UNSET
    """Desired throttle key dimension, optional rate ceiling, and missing-identity behavior."""
    budget: RouteBudgetRequirement | Unset = UNSET
    """Optional execution-budget ceiling or requirement for an explicit budget rule."""

    def to_dict(self) -> dict[str, Any]:
        authentication: str | Unset = UNSET
        if not isinstance(self.authentication, Unset):
            authentication = self.authentication

        throttle: dict[str, Any] | Unset = UNSET
        if not isinstance(self.throttle, Unset):
            throttle = self.throttle.to_dict()

        budget: dict[str, Any] | Unset = UNSET
        if not isinstance(self.budget, Unset):
            budget = self.budget.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if authentication is not UNSET:
            field_dict["authentication"] = authentication
        if throttle is not UNSET:
            field_dict["throttle"] = throttle
        if budget is not UNSET:
            field_dict["budget"] = budget

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_budget_requirement import RouteBudgetRequirement
        from ..models.route_throttle_requirement import RouteThrottleRequirement

        d = dict(src_dict)
        _authentication = d.pop("authentication", UNSET)
        authentication: RouteChecksAuthentication | Unset
        if isinstance(_authentication, Unset):
            authentication = UNSET
        else:
            authentication = check_route_checks_authentication(_authentication)

        _throttle = d.pop("throttle", UNSET)
        throttle: RouteThrottleRequirement | Unset
        if isinstance(_throttle, Unset):
            throttle = UNSET
        else:
            throttle = RouteThrottleRequirement.from_dict(_throttle)

        _budget = d.pop("budget", UNSET)
        budget: RouteBudgetRequirement | Unset
        if isinstance(_budget, Unset):
            budget = UNSET
        else:
            budget = RouteBudgetRequirement.from_dict(_budget)

        route_checks = cls(
            authentication=authentication,
            throttle=throttle,
            budget=budget,
        )

        return route_checks
