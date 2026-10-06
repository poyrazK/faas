from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="BindingPromotionRequest")


@_attrs_define
class BindingPromotionRequest:
    """Policy for a server-enforced bindings promotion; the gate is always required on this route. Stored scope policy may
    require a shorter age or application ACKs and disallow unsupported waivers. The response reports the effective
    policy.

    """

    expected_serving_deployment_id: UUID | Unset = UNSET
    max_verification_age: str | Unset = "10m"
    """Positive Go duration; maximum age of passed verification evidence."""
    allow_unsupported: bool | Unset = False
    """Explicit queue/outbound connectivity probe waiver; coverage remains partial. Push consumer readiness and the
    independent 30-second poll window cannot be waived."""
    require_application_ack: bool | Unset = False
    """Require current managed-secret application receipts; use the dedicated promote-with-application-ack route
    for compatibility with older servers. The dedicated route always forces true."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected_serving_deployment_id: str | Unset = UNSET
        if not isinstance(self.expected_serving_deployment_id, Unset):
            expected_serving_deployment_id = str(self.expected_serving_deployment_id)

        max_verification_age = self.max_verification_age

        allow_unsupported = self.allow_unsupported

        require_application_ack = self.require_application_ack

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if expected_serving_deployment_id is not UNSET:
            field_dict["expected_serving_deployment_id"] = expected_serving_deployment_id
        if max_verification_age is not UNSET:
            field_dict["max_verification_age"] = max_verification_age
        if allow_unsupported is not UNSET:
            field_dict["allow_unsupported"] = allow_unsupported
        if require_application_ack is not UNSET:
            field_dict["require_application_ack"] = require_application_ack

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _expected_serving_deployment_id = d.pop("expected_serving_deployment_id", UNSET)
        expected_serving_deployment_id: UUID | Unset
        if isinstance(_expected_serving_deployment_id, Unset):
            expected_serving_deployment_id = UNSET
        else:
            expected_serving_deployment_id = UUID(_expected_serving_deployment_id)

        max_verification_age = d.pop("max_verification_age", UNSET)

        allow_unsupported = d.pop("allow_unsupported", UNSET)

        require_application_ack = d.pop("require_application_ack", UNSET)

        binding_promotion_request = cls(
            expected_serving_deployment_id=expected_serving_deployment_id,
            max_verification_age=max_verification_age,
            allow_unsupported=allow_unsupported,
            require_application_ack=require_application_ack,
        )

        binding_promotion_request.additional_properties = d
        return binding_promotion_request

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
