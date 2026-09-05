from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.update_operator_runtime_config_body_scope import (
    UpdateOperatorRuntimeConfigBodyScope,
    check_update_operator_runtime_config_body_scope,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdateOperatorRuntimeConfigBody")


@_attrs_define
class UpdateOperatorRuntimeConfigBody:
    value: Any
    reason: str
    expected_version: int | Unset = UNSET
    scope: UpdateOperatorRuntimeConfigBodyScope | Unset = "global"
    scope_id: str | Unset = UNSET
    rollout_percent: int | Unset = 100
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        value = self.value

        reason = self.reason

        expected_version = self.expected_version

        scope: str | Unset = UNSET
        if not isinstance(self.scope, Unset):
            scope = self.scope

        scope_id = self.scope_id

        rollout_percent = self.rollout_percent

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "value": value,
                "reason": reason,
            }
        )
        if expected_version is not UNSET:
            field_dict["expected_version"] = expected_version
        if scope is not UNSET:
            field_dict["scope"] = scope
        if scope_id is not UNSET:
            field_dict["scope_id"] = scope_id
        if rollout_percent is not UNSET:
            field_dict["rollout_percent"] = rollout_percent

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        value = d.pop("value")

        reason = d.pop("reason")

        expected_version = d.pop("expected_version", UNSET)

        _scope = d.pop("scope", UNSET)
        scope: UpdateOperatorRuntimeConfigBodyScope | Unset
        if isinstance(_scope, Unset):
            scope = UNSET
        else:
            scope = check_update_operator_runtime_config_body_scope(_scope)

        scope_id = d.pop("scope_id", UNSET)

        rollout_percent = d.pop("rollout_percent", UNSET)

        update_operator_runtime_config_body = cls(
            value=value,
            reason=reason,
            expected_version=expected_version,
            scope=scope,
            scope_id=scope_id,
            rollout_percent=rollout_percent,
        )

        update_operator_runtime_config_body.additional_properties = d
        return update_operator_runtime_config_body

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
