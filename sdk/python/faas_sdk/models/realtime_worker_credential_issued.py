from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.realtime_worker_credential import RealtimeWorkerCredential


T = TypeVar("T", bound="RealtimeWorkerCredentialIssued")


@_attrs_define
class RealtimeWorkerCredentialIssued:
    """Realtime Worker Credential Issued."""

    credential: str
    """Plaintext grw_ credential, returned once; never log or cache."""
    info: RealtimeWorkerCredential
    """Realtime Worker Credential."""
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
        from ..models.realtime_worker_credential import RealtimeWorkerCredential

        d = dict(src_dict)
        credential = d.pop("credential")

        info = RealtimeWorkerCredential.from_dict(d.pop("info"))

        realtime_worker_credential_issued = cls(
            credential=credential,
            info=info,
        )

        realtime_worker_credential_issued.additional_properties = d
        return realtime_worker_credential_issued

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
