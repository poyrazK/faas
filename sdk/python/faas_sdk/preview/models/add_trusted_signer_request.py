from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AddTrustedSignerRequest")


@_attrs_define
class AddTrustedSignerRequest:
    """PUT body for `/v1/apps/{slug}/trusted_signers/{name}`. `public_key_pem` is
    the base64-encoded DER blob (apid side strips PEM armour). The DER must
    parse as an ECDSA P-256 SPKI; any other curve or non-ECDSA key returns
    400 `trusted_signer_invalid`. Bytes length must land in [64, 1024].

    """

    public_key_pem: str
    """Base64-encoded DER SubjectPublicKeyInfo. Length bounds match the DB CHECK."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        public_key_pem = self.public_key_pem

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "public_key_pem": public_key_pem,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        public_key_pem = d.pop("public_key_pem")

        add_trusted_signer_request = cls(
            public_key_pem=public_key_pem,
        )

        add_trusted_signer_request.additional_properties = d
        return add_trusted_signer_request

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
