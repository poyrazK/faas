from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AppSecretExportResponse")


@_attrs_define
class AppSecretExportResponse:
    """One sealed secret in export form: key name, sealed ciphertext (sealed at rest per §11), version, and timestamps.
    Plaintext is never exported.

    """

    app_id: str
    key: str
    ciphertext: str
    """ base64-encoded age-sealed envelope """
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        key = self.key

        ciphertext = self.ciphertext

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "key": key,
                "ciphertext": ciphertext,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        key = d.pop("key")

        ciphertext = d.pop("ciphertext")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        app_secret_export_response = cls(
            app_id=app_id,
            key=key,
            ciphertext=ciphertext,
            created_at=created_at,
            updated_at=updated_at,
        )

        app_secret_export_response.additional_properties = d
        return app_secret_export_response

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
