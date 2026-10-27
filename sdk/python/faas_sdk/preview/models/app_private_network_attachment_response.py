from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.app_private_network_attachment import AppPrivateNetworkAttachment


T = TypeVar("T", bound="AppPrivateNetworkAttachmentResponse")


@_attrs_define
class AppPrivateNetworkAttachmentResponse:
    """Capability metadata and the current private-network intent."""

    feature_enabled: bool
    plan_allowed: bool
    max_cidrs: int
    attachment: AppPrivateNetworkAttachment | None
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.app_private_network_attachment import AppPrivateNetworkAttachment

        feature_enabled = self.feature_enabled

        plan_allowed = self.plan_allowed

        max_cidrs = self.max_cidrs

        attachment: dict[str, Any] | None
        if isinstance(self.attachment, AppPrivateNetworkAttachment):
            attachment = self.attachment.to_dict()
        else:
            attachment = self.attachment

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "feature_enabled": feature_enabled,
                "plan_allowed": plan_allowed,
                "max_cidrs": max_cidrs,
                "attachment": attachment,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_private_network_attachment import AppPrivateNetworkAttachment

        d = dict(src_dict)
        feature_enabled = d.pop("feature_enabled")

        plan_allowed = d.pop("plan_allowed")

        max_cidrs = d.pop("max_cidrs")

        def _parse_attachment(data: object) -> AppPrivateNetworkAttachment | None:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                attachment_type_0 = AppPrivateNetworkAttachment.from_dict(data)

                return attachment_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(AppPrivateNetworkAttachment | None, data)

        attachment = _parse_attachment(d.pop("attachment"))

        app_private_network_attachment_response = cls(
            feature_enabled=feature_enabled,
            plan_allowed=plan_allowed,
            max_cidrs=max_cidrs,
            attachment=attachment,
        )

        app_private_network_attachment_response.additional_properties = d
        return app_private_network_attachment_response

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
