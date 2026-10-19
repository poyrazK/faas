from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.application_standard_log_destination_kind import (
    ApplicationStandardLogDestinationKind,
    check_application_standard_log_destination_kind,
)

T = TypeVar("T", bound="ApplicationStandardLogDestination")


@_attrs_define
class ApplicationStandardLogDestination:
    """An immutable logging endpoint with credential presence and a configuration hash."""

    id: UUID
    org_id: UUID
    name: str
    kind: ApplicationStandardLogDestinationKind
    target_url: str
    has_auth_header: bool
    config_hash: str
    created_by: UUID
    created_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        org_id = str(self.org_id)

        name = self.name

        kind: str = self.kind

        target_url = self.target_url

        has_auth_header = self.has_auth_header

        config_hash = self.config_hash

        created_by = str(self.created_by)

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "org_id": org_id,
                "name": name,
                "kind": kind,
                "target_url": target_url,
                "has_auth_header": has_auth_header,
                "config_hash": config_hash,
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

        kind = check_application_standard_log_destination_kind(d.pop("kind"))

        target_url = d.pop("target_url")

        has_auth_header = d.pop("has_auth_header")

        config_hash = d.pop("config_hash")

        created_by = UUID(d.pop("created_by"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        application_standard_log_destination = cls(
            id=id,
            org_id=org_id,
            name=name,
            kind=kind,
            target_url=target_url,
            has_auth_header=has_auth_header,
            config_hash=config_hash,
            created_by=created_by,
            created_at=created_at,
        )

        return application_standard_log_destination
