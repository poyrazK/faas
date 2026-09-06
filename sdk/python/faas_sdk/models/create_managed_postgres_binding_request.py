from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.create_managed_postgres_binding_request_access import (
    CreateManagedPostgresBindingRequestAccess,
    check_create_managed_postgres_binding_request_access,
)

T = TypeVar("T", bound="CreateManagedPostgresBindingRequest")


@_attrs_define
class CreateManagedPostgresBindingRequest:
    """Request to inject a managed database credential into an app environment."""

    app_id: str
    scope: str
    environment_key: str
    access: CreateManagedPostgresBindingRequestAccess

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        scope = self.scope

        environment_key = self.environment_key

        access: str = self.access

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "scope": scope,
                "environment_key": environment_key,
                "access": access,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        scope = d.pop("scope")

        environment_key = d.pop("environment_key")

        access = check_create_managed_postgres_binding_request_access(d.pop("access"))

        create_managed_postgres_binding_request = cls(
            app_id=app_id,
            scope=scope,
            environment_key=environment_key,
            access=access,
        )

        return create_managed_postgres_binding_request
