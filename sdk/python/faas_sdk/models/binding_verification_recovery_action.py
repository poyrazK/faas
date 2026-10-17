from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.binding_verification_recovery_action_action import (
    BindingVerificationRecoveryActionAction,
    check_binding_verification_recovery_action_action,
)

T = TypeVar("T", bound="BindingVerificationRecoveryAction")


@_attrs_define
class BindingVerificationRecoveryAction:
    """Suggested client recovery step for a verification receipt; it grants no execution authority."""

    action: BindingVerificationRecoveryActionAction
    message: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        action: str = self.action

        message = self.message

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "action": action,
                "message": message,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action = check_binding_verification_recovery_action_action(d.pop("action"))

        message = d.pop("message")

        binding_verification_recovery_action = cls(
            action=action,
            message=message,
        )

        binding_verification_recovery_action.additional_properties = d
        return binding_verification_recovery_action

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
