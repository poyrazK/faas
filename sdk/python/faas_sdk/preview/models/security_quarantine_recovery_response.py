from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.security_quarantine_recovery_response_status import (
    SecurityQuarantineRecoveryResponseStatus,
    check_security_quarantine_recovery_response_status,
)

T = TypeVar("T", bound="SecurityQuarantineRecoveryResponse")


@_attrs_define
class SecurityQuarantineRecoveryResponse:
    """The app's post-recovery active state."""

    app_id: str
    slug: str
    deployment_id: str
    image_digest: str
    recovered_at: datetime.datetime
    status: SecurityQuarantineRecoveryResponseStatus
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        slug = self.slug

        deployment_id = self.deployment_id

        image_digest = self.image_digest

        recovered_at = self.recovered_at.isoformat()

        status: str = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "slug": slug,
                "deployment_id": deployment_id,
                "image_digest": image_digest,
                "recovered_at": recovered_at,
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        slug = d.pop("slug")

        deployment_id = d.pop("deployment_id")

        image_digest = d.pop("image_digest")

        recovered_at = datetime.datetime.fromisoformat(d.pop("recovered_at"))

        status = check_security_quarantine_recovery_response_status(d.pop("status"))

        security_quarantine_recovery_response = cls(
            app_id=app_id,
            slug=slug,
            deployment_id=deployment_id,
            image_digest=image_digest,
            recovered_at=recovered_at,
            status=status,
        )

        security_quarantine_recovery_response.additional_properties = d
        return security_quarantine_recovery_response

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
