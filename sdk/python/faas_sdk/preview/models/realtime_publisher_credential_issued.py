from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.realtime_publisher_credential import RealtimePublisherCredential


T = TypeVar("T", bound="RealtimePublisherCredentialIssued")


@_attrs_define
class RealtimePublisherCredentialIssued:
    """Realtime Publisher Credential Issued."""

    credential: str
    """One-time plaintext grp_ credential; never log or cache."""
    info: RealtimePublisherCredential
    """Realtime Publisher Credential."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        credential = self.credential

        info = self.info.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "credential": credential,
                "info": info,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_publisher_credential import RealtimePublisherCredential

        d = dict(src_dict)
        credential = d.pop("credential")

        info = RealtimePublisherCredential.from_dict(d.pop("info"))

        realtime_publisher_credential_issued = cls(
            credential=credential,
            info=info,
        )

        realtime_publisher_credential_issued.additional_properties = d
        return realtime_publisher_credential_issued

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
