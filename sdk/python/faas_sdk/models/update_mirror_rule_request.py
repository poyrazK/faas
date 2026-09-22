from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdateMirrorRuleRequest")


@_attrs_define
class UpdateMirrorRuleRequest:
    """Body for PATCH /v1/apps/{slug}/mirrors/{id}. All fields are
    optional; pointer-style patches mean an absent key keeps the
    existing value, while an explicit zero/empty overrides. Setting
    `redact_headers` to `[]` clears the customer's list (leaving
    only the always-stripped list).

    """

    percent: int | Unset = UNSET
    """New fan-out percent in [0, 100]. 0 = rule stays but doesn't fire."""
    enabled: bool | Unset = UNSET
    """Set false to pause the rule without removing it."""
    include_body: bool | Unset = UNSET
    """Toggle request/response body-hash comparison in the ledger. Raw bodies are never stored."""
    redact_headers: list[str] | Unset = UNSET
    """Replace the customer's redact list. Empty array clears it."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        percent = self.percent

        enabled = self.enabled

        include_body = self.include_body

        redact_headers: list[str] | Unset = UNSET
        if not isinstance(self.redact_headers, Unset):
            redact_headers = self.redact_headers

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if percent is not UNSET:
            field_dict["percent"] = percent
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if include_body is not UNSET:
            field_dict["include_body"] = include_body
        if redact_headers is not UNSET:
            field_dict["redact_headers"] = redact_headers

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        percent = d.pop("percent", UNSET)

        enabled = d.pop("enabled", UNSET)

        include_body = d.pop("include_body", UNSET)

        redact_headers = cast(list[str], d.pop("redact_headers", UNSET))

        update_mirror_rule_request = cls(
            percent=percent,
            enabled=enabled,
            include_body=include_body,
            redact_headers=redact_headers,
        )

        update_mirror_rule_request.additional_properties = d
        return update_mirror_rule_request

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
