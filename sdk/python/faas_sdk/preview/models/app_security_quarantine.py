from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_security_quarantine_reason import AppSecurityQuarantineReason, check_app_security_quarantine_reason
from ..types import UNSET, Unset

T = TypeVar("T", bound="AppSecurityQuarantine")


@_attrs_define
class AppSecurityQuarantine:
    """Active image-scan quarantine details. Vulnerability findings remain on the deployment scan resource."""

    deployment_id: str
    """Deployment whose scan regression caused the quarantine."""
    image_digest: str
    """Immutable image digest that was quarantined."""
    reason: AppSecurityQuarantineReason
    parked_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = self.deployment_id

        image_digest = self.image_digest

        reason: str = self.reason

        parked_at: None | str | Unset
        if isinstance(self.parked_at, Unset):
            parked_at = UNSET
        elif isinstance(self.parked_at, datetime.datetime):
            parked_at = self.parked_at.isoformat()
        else:
            parked_at = self.parked_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "image_digest": image_digest,
                "reason": reason,
            }
        )
        if parked_at is not UNSET:
            field_dict["parked_at"] = parked_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = d.pop("deployment_id")

        image_digest = d.pop("image_digest")

        reason = check_app_security_quarantine_reason(d.pop("reason"))

        def _parse_parked_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                parked_at_type_0 = datetime.datetime.fromisoformat(data)

                return parked_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        parked_at = _parse_parked_at(d.pop("parked_at", UNSET))

        app_security_quarantine = cls(
            deployment_id=deployment_id,
            image_digest=image_digest,
            reason=reason,
            parked_at=parked_at,
        )

        app_security_quarantine.additional_properties = d
        return app_security_quarantine

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
