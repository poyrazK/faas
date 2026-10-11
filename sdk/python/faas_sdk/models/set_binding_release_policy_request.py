from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.set_binding_release_policy_request_mode import (
    SetBindingReleasePolicyRequestMode,
    check_set_binding_release_policy_request_mode,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="SetBindingReleasePolicyRequest")


@_attrs_define
class SetBindingReleasePolicyRequest:
    """Compare-and-set binding release enforcement for one deployment scope, including evidence age, application
    acknowledgements and an explicit change reason.

    """

    mode: SetBindingReleasePolicyRequestMode
    expected_revision: int
    max_verification_age: str | Unset = "10m"
    """Whole-second Go duration between 1s and 24h."""
    require_application_ack: bool | Unset = False
    reason: str | Unset = UNSET
    """Required for off mode; single line, at most 256 UTF-8 bytes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        expected_revision = self.expected_revision

        max_verification_age = self.max_verification_age

        require_application_ack = self.require_application_ack

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "mode": mode,
                "expected_revision": expected_revision,
            }
        )
        if max_verification_age is not UNSET:
            field_dict["max_verification_age"] = max_verification_age
        if require_application_ack is not UNSET:
            field_dict["require_application_ack"] = require_application_ack
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_set_binding_release_policy_request_mode(d.pop("mode"))

        expected_revision = d.pop("expected_revision")

        max_verification_age = d.pop("max_verification_age", UNSET)

        require_application_ack = d.pop("require_application_ack", UNSET)

        reason = d.pop("reason", UNSET)

        set_binding_release_policy_request = cls(
            mode=mode,
            expected_revision=expected_revision,
            max_verification_age=max_verification_age,
            require_application_ack=require_application_ack,
            reason=reason,
        )

        set_binding_release_policy_request.additional_properties = d
        return set_binding_release_policy_request

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
