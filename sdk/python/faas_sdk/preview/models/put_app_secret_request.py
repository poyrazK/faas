from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.put_app_secret_request_secret_class import (
    PutAppSecretRequestSecretClass,
    check_put_app_secret_request_secret_class,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PutAppSecretRequest")


@_attrs_define
class PutAppSecretRequest:
    """Set a secret: key name and plaintext (sealed at rest immediately, plaintext discarded after seal). secret_class is
    optional; omission preserves an existing class and defaults new rows to persistent. Ephemeral values prevent future
    init/warm captures for the app scope; older artifacts age out under normal snapshot garbage collection.

    """

    value: str
    """Plaintext. Sealed server-side; never persisted in plaintext."""
    secret_class: PutAppSecretRequestSecretClass | Unset = UNSET
    """Optional snapshot-retention policy. Ephemeral secrets cause their runtime VM to be destroyed instead of
    creating warm or init snapshots when parked; new requests cold-boot."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        value = self.value

        secret_class: str | Unset = UNSET
        if not isinstance(self.secret_class, Unset):
            secret_class = self.secret_class

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "value": value,
            }
        )
        if secret_class is not UNSET:
            field_dict["secret_class"] = secret_class

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        value = d.pop("value")

        _secret_class = d.pop("secret_class", UNSET)
        secret_class: PutAppSecretRequestSecretClass | Unset
        if isinstance(_secret_class, Unset):
            secret_class = UNSET
        else:
            secret_class = check_put_app_secret_request_secret_class(_secret_class)

        put_app_secret_request = cls(
            value=value,
            secret_class=secret_class,
        )

        put_app_secret_request.additional_properties = d
        return put_app_secret_request

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
