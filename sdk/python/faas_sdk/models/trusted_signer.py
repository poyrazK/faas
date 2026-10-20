from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="TrustedSigner")


@_attrs_define
class TrustedSigner:
    """One entry in the per-app cosign trusted-publisher list. `name` is the
    apid-side label (matches `app_trusted_signers.signer_name`); `public_key_pem`
    is the base64-encoded DER SubjectPublicKeyInfo bytes — NOT a PEM-armoured
    block. The wire form strips the PEM armour at upload time so the
    on-disk mirror (imaged reads `/etc/faas/secrets/trusted-publishers/`) is
    a single blob per publisher. ECDSA P-256 only (ADR-038); apid rejects
    other curves at PUT time with `trusted_signer_invalid`.

    """

    name: str
    """Lower-case label. Matches `app_trusted_signers.signer_name`. The label is the on-disk filename (without
    .pem) under /etc/faas/secrets/trusted-publishers/."""
    public_key_pem: str
    """Base64-encoded DER SubjectPublicKeyInfo. Bytes length must be in [64, 1024] (DB CHECK)."""
    added_at: datetime.datetime
    """RFC3339 timestamp of when the operator onboarded this publisher."""
    added_by: str | Unset = UNSET
    """Account ID of the admin who onboarded this publisher (omit when not yet in audit log)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        public_key_pem = self.public_key_pem

        added_at = self.added_at.isoformat()

        added_by = self.added_by

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "public_key_pem": public_key_pem,
                "added_at": added_at,
            }
        )
        if added_by is not UNSET:
            field_dict["added_by"] = added_by

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        public_key_pem = d.pop("public_key_pem")

        added_at = datetime.datetime.fromisoformat(d.pop("added_at"))

        added_by = d.pop("added_by", UNSET)

        trusted_signer = cls(
            name=name,
            public_key_pem=public_key_pem,
            added_at=added_at,
            added_by=added_by,
        )

        trusted_signer.additional_properties = d
        return trusted_signer

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
