from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="MigrationServiceConnectionCheck")


@_attrs_define
class MigrationServiceConnectionCheck:
    """Runtime variable and logical service pinned to an exact target deployment. The guest checks its private HTTPS value
    and authorized routing; no URLs or credentials are saved.

    """

    env_key: str
    service: str
    deployment_id: UUID

    def to_dict(self) -> dict[str, Any]:
        env_key = self.env_key

        service = self.service

        deployment_id = str(self.deployment_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "env_key": env_key,
                "service": service,
                "deployment_id": deployment_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        env_key = d.pop("env_key")

        service = d.pop("service")

        deployment_id = UUID(d.pop("deployment_id"))

        migration_service_connection_check = cls(
            env_key=env_key,
            service=service,
            deployment_id=deployment_id,
        )

        return migration_service_connection_check
