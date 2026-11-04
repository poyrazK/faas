from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_open_api_policy_preview_rule_action import AppOpenAPIPolicyPreviewRuleAction


T = TypeVar("T", bound="AppOpenAPIPolicyPreviewRule")


@_attrs_define
class AppOpenAPIPolicyPreviewRule:
    """Read-only edge-rule subset explaining why a preview route is covered."""

    id: UUID
    match_host: str
    match_path: str
    match_methods: list[str]
    priority: int
    enabled: bool
    kind: str
    action: AppOpenAPIPolicyPreviewRuleAction
    validate_mode: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        match_host = self.match_host

        match_path = self.match_path

        match_methods = self.match_methods

        priority = self.priority

        enabled = self.enabled

        kind = self.kind

        action = self.action.to_dict()

        validate_mode = self.validate_mode

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "match_host": match_host,
                "match_path": match_path,
                "match_methods": match_methods,
                "priority": priority,
                "enabled": enabled,
                "kind": kind,
                "action": action,
            }
        )
        if validate_mode is not UNSET:
            field_dict["validate_mode"] = validate_mode

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_open_api_policy_preview_rule_action import AppOpenAPIPolicyPreviewRuleAction

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        match_host = d.pop("match_host")

        match_path = d.pop("match_path")

        match_methods = cast(list[str], d.pop("match_methods"))

        priority = d.pop("priority")

        enabled = d.pop("enabled")

        kind = d.pop("kind")

        action = AppOpenAPIPolicyPreviewRuleAction.from_dict(d.pop("action"))

        validate_mode = d.pop("validate_mode", UNSET)

        app_open_api_policy_preview_rule = cls(
            id=id,
            match_host=match_host,
            match_path=match_path,
            match_methods=match_methods,
            priority=priority,
            enabled=enabled,
            kind=kind,
            action=action,
            validate_mode=validate_mode,
        )

        app_open_api_policy_preview_rule.additional_properties = d
        return app_open_api_policy_preview_rule

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
