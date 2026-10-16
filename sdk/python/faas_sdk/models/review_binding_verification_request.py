from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.review_binding_verification_request_service_targets import (
        ReviewBindingVerificationRequestServiceTargets,
    )


T = TypeVar("T", bound="ReviewBindingVerificationRequest")


@_attrs_define
class ReviewBindingVerificationRequest:
    """Application deployment and binding selection to review without admitting work."""

    deployment_id: UUID | Unset = UNSET
    """Exact deployment; omission selects the current live deployment once."""
    service_targets: ReviewBindingVerificationRequestServiceTargets | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        service_targets: dict[str, Any] | Unset = UNSET
        if not isinstance(self.service_targets, Unset):
            service_targets = self.service_targets.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if service_targets is not UNSET:
            field_dict["service_targets"] = service_targets

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.review_binding_verification_request_service_targets import (
            ReviewBindingVerificationRequestServiceTargets,
        )

        d = dict(src_dict)
        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        _service_targets = d.pop("service_targets", UNSET)
        service_targets: ReviewBindingVerificationRequestServiceTargets | Unset
        if isinstance(_service_targets, Unset):
            service_targets = UNSET
        else:
            service_targets = ReviewBindingVerificationRequestServiceTargets.from_dict(_service_targets)

        review_binding_verification_request = cls(
            deployment_id=deployment_id,
            service_targets=service_targets,
        )

        review_binding_verification_request.additional_properties = d
        return review_binding_verification_request

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
