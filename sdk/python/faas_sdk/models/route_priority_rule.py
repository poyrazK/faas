from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.route_priority_rule_class import RoutePriorityRuleClass, check_route_priority_rule_class
from ..models.route_priority_rule_method import RoutePriorityRuleMethod, check_route_priority_rule_method
from ..types import UNSET, Unset

T = TypeVar("T", bound="RoutePriorityRule")


@_attrs_define
class RoutePriorityRule:
    """Assigns a priority class to requests matching a method and path (ADR-947)."""

    path: str
    """Route template such as /users/{id}, or an edge-rule glob such as /exports/*."""
    class_: RoutePriorityRuleClass
    method: RoutePriorityRuleMethod | Unset = UNSET
    """Omitted matches every method."""

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        class_: str = self.class_

        method: str | Unset = UNSET
        if not isinstance(self.method, Unset):
            method = self.method

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "path": path,
                "class": class_,
            }
        )
        if method is not UNSET:
            field_dict["method"] = method

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        path = d.pop("path")

        class_ = check_route_priority_rule_class(d.pop("class"))

        _method = d.pop("method", UNSET)
        method: RoutePriorityRuleMethod | Unset
        if isinstance(_method, Unset):
            method = UNSET
        else:
            method = check_route_priority_rule_method(_method)

        route_priority_rule = cls(
            path=path,
            class_=class_,
            method=method,
        )

        return route_priority_rule
