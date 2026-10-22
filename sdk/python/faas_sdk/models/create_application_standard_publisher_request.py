from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="CreateApplicationStandardPublisherRequest")


@_attrs_define
class CreateApplicationStandardPublisherRequest:
    """An approved ECDSA P-256 public signing key."""

    name: str
    public_key_der: str
    """Base64 ECDSA P-256 SubjectPublicKeyInfo DER; private keys and unsupported curves are rejected."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        public_key_der = self.public_key_der

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "public_key_der": public_key_der,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        public_key_der = d.pop("public_key_der")

        create_application_standard_publisher_request = cls(
            name=name,
            public_key_der=public_key_der,
        )

        return create_application_standard_publisher_request
