from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_open_api_policy_preview_route_method import (
    AppOpenAPIPolicyPreviewRouteMethod,
    check_app_open_api_policy_preview_route_method,
)
from ..models.app_open_api_policy_preview_route_status import (
    AppOpenAPIPolicyPreviewRouteStatus,
    check_app_open_api_policy_preview_route_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_open_api_policy_preview_rule import AppOpenAPIPolicyPreviewRule


T = TypeVar("T", bound="AppOpenAPIPolicyPreviewRoute")


@_attrs_define
class AppOpenAPIPolicyPreviewRoute:
    """One declared/observed path-method row with policy coverage and drift status."""

    path: str
    method: AppOpenAPIPolicyPreviewRouteMethod
    status: AppOpenAPIPolicyPreviewRouteStatus
    declared: bool
    observed: bool
    covered: bool
    """True when at least one enabled edge rule matches this path and method."""
    rules: list[AppOpenAPIPolicyPreviewRule] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        method: str = self.method

        status: str = self.status

        declared = self.declared

        observed = self.observed

        covered = self.covered

        rules: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.rules, Unset):
            rules = []
            for rules_item_data in self.rules:
                rules_item = rules_item_data.to_dict()
                rules.append(rules_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "path": path,
                "method": method,
                "status": status,
                "declared": declared,
                "observed": observed,
                "covered": covered,
            }
        )
        if rules is not UNSET:
            field_dict["rules"] = rules

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_open_api_policy_preview_rule import AppOpenAPIPolicyPreviewRule

        d = dict(src_dict)
        path = d.pop("path")

        method = check_app_open_api_policy_preview_route_method(d.pop("method"))

        status = check_app_open_api_policy_preview_route_status(d.pop("status"))

        declared = d.pop("declared")

        observed = d.pop("observed")

        covered = d.pop("covered")

        _rules = d.pop("rules", UNSET)
        rules: list[AppOpenAPIPolicyPreviewRule] | Unset = UNSET
        if _rules is not UNSET:
            rules = []
            for rules_item_data in _rules:
                rules_item = AppOpenAPIPolicyPreviewRule.from_dict(rules_item_data)

                rules.append(rules_item)

        app_open_api_policy_preview_route = cls(
            path=path,
            method=method,
            status=status,
            declared=declared,
            observed=observed,
            covered=covered,
            rules=rules,
        )

        app_open_api_policy_preview_route.additional_properties = d
        return app_open_api_policy_preview_route

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
