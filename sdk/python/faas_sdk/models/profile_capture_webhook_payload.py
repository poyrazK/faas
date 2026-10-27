from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.profile_capture import ProfileCapture


T = TypeVar("T", bound="ProfileCaptureWebhookPayload")


@_attrs_define
class ProfileCaptureWebhookPayload:
    """Payload of the profile.capture_ready and profile.capture_oom_linked app webhook events. Carries the capture record
    and its paths, never profile samples.

    """

    version: int
    capture: ProfileCapture
    """An on-demand capture. processes counts instrumented processes that saw the capture; zero means the instance
    has no collector."""
    capture_path: str
    """Dashboard page of the capture."""
    api_path: str
    """API resource of the capture."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        capture = self.capture.to_dict()

        capture_path = self.capture_path

        api_path = self.api_path

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "capture": capture,
                "capture_path": capture_path,
                "api_path": api_path,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_capture import ProfileCapture

        d = dict(src_dict)
        version = d.pop("version")

        capture = ProfileCapture.from_dict(d.pop("capture"))

        capture_path = d.pop("capture_path")

        api_path = d.pop("api_path")

        profile_capture_webhook_payload = cls(
            version=version,
            capture=capture,
            capture_path=capture_path,
            api_path=api_path,
        )

        profile_capture_webhook_payload.additional_properties = d
        return profile_capture_webhook_payload

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
