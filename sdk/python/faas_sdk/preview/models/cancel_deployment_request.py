from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.cancel_deployment_request_reason import (
    CancelDeploymentRequestReason,
    check_cancel_deployment_request_reason,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CancelDeploymentRequest")


@_attrs_define
class CancelDeploymentRequest:
    """Body for POST /v1/apps/{slug}/deployments/{id}/cancel (ADR-124). Optional — empty body defaults to reason='user'
    server-side. Reason is the closed set user | auto_quota | auto_health | system.

    """

    reason: CancelDeploymentRequestReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _reason = d.pop("reason", UNSET)
        reason: CancelDeploymentRequestReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_cancel_deployment_request_reason(_reason)

        cancel_deployment_request = cls(
            reason=reason,
        )

        cancel_deployment_request.additional_properties = d
        return cancel_deployment_request

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
