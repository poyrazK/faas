from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.migration_cutover_action_request_action import (
    MigrationCutoverActionRequestAction,
    check_migration_cutover_action_request_action,
)

T = TypeVar("T", bound="MigrationCutoverActionRequest")


@_attrs_define
class MigrationCutoverActionRequest:
    """Explicit user action. Restored records the customer attestation that provider DNS was restored; Gregale never edits
    external DNS.

    """

    action: MigrationCutoverActionRequestAction
    acknowledge_limited_traffic: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        action: str = self.action

        acknowledge_limited_traffic = self.acknowledge_limited_traffic

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "action": action,
                "acknowledge_limited_traffic": acknowledge_limited_traffic,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action = check_migration_cutover_action_request_action(d.pop("action"))

        acknowledge_limited_traffic = d.pop("acknowledge_limited_traffic")

        migration_cutover_action_request = cls(
            action=action,
            acknowledge_limited_traffic=acknowledge_limited_traffic,
        )

        migration_cutover_action_request.additional_properties = d
        return migration_cutover_action_request

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
