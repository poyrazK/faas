from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="ApplicationStandardPublisher")


@_attrs_define
class ApplicationStandardPublisher:
    """An immutable signing key with its public fingerprint."""

    id: UUID
    org_id: UUID
    name: str
    public_key_der: str
    fingerprint: str
    created_by: UUID
    created_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        org_id = str(self.org_id)

        name = self.name

        public_key_der = self.public_key_der

        fingerprint = self.fingerprint

        created_by = str(self.created_by)

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "org_id": org_id,
                "name": name,
                "public_key_der": public_key_der,
                "fingerprint": fingerprint,
                "created_by": created_by,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        org_id = UUID(d.pop("org_id"))

        name = d.pop("name")

        public_key_der = d.pop("public_key_der")

        fingerprint = d.pop("fingerprint")

        created_by = UUID(d.pop("created_by"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        application_standard_publisher = cls(
            id=id,
            org_id=org_id,
            name=name,
            public_key_der=public_key_der,
            fingerprint=fingerprint,
            created_by=created_by,
            created_at=created_at,
        )

        return application_standard_publisher
