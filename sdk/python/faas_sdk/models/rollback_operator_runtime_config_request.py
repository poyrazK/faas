from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.rollback_operator_runtime_config_request_scope import (
    RollbackOperatorRuntimeConfigRequestScope,
    check_rollback_operator_runtime_config_request_scope,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RollbackOperatorRuntimeConfigRequest")


@_attrs_define
class RollbackOperatorRuntimeConfigRequest:
    """Request to apply a historical runtime configuration revision."""

    version: int
    reason: str
    expected_version: int | Unset = UNSET
    scope: RollbackOperatorRuntimeConfigRequestScope | Unset = "global"
    scope_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        reason = self.reason

        expected_version = self.expected_version

        scope: str | Unset = UNSET
        if not isinstance(self.scope, Unset):
            scope = self.scope

        scope_id = self.scope_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "reason": reason,
            }
        )
        if expected_version is not UNSET:
            field_dict["expected_version"] = expected_version
        if scope is not UNSET:
            field_dict["scope"] = scope
        if scope_id is not UNSET:
            field_dict["scope_id"] = scope_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = d.pop("version")

        reason = d.pop("reason")

        expected_version = d.pop("expected_version", UNSET)

        _scope = d.pop("scope", UNSET)
        scope: RollbackOperatorRuntimeConfigRequestScope | Unset
        if isinstance(_scope, Unset):
            scope = UNSET
        else:
            scope = check_rollback_operator_runtime_config_request_scope(_scope)

        scope_id = d.pop("scope_id", UNSET)

        rollback_operator_runtime_config_request = cls(
            version=version,
            reason=reason,
            expected_version=expected_version,
            scope=scope,
            scope_id=scope_id,
        )

        rollback_operator_runtime_config_request.additional_properties = d
        return rollback_operator_runtime_config_request

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
