from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_binding_access import ManagedPostgresBindingAccess, check_managed_postgres_binding_access
from ..models.managed_postgres_binding_state import ManagedPostgresBindingState, check_managed_postgres_binding_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresBinding")


@_attrs_define
class ManagedPostgresBinding:
    """Workload binding metadata. Credentials are delivered through the app secret surface and never returned here."""

    id: str
    database_id: str
    app_id: str
    scope: str
    environment_key: str
    access: ManagedPostgresBindingAccess
    credential_generation: int
    state: ManagedPostgresBindingState
    created_at: datetime.datetime
    updated_at: datetime.datetime
    last_error_code: None | str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        database_id = self.database_id

        app_id = self.app_id

        scope = self.scope

        environment_key = self.environment_key

        access: str = self.access

        credential_generation = self.credential_generation

        state: str = self.state

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        last_error_code: None | str | Unset
        if isinstance(self.last_error_code, Unset):
            last_error_code = UNSET
        else:
            last_error_code = self.last_error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "database_id": database_id,
                "app_id": app_id,
                "scope": scope,
                "environment_key": environment_key,
                "access": access,
                "credential_generation": credential_generation,
                "state": state,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if last_error_code is not UNSET:
            field_dict["last_error_code"] = last_error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        database_id = d.pop("database_id")

        app_id = d.pop("app_id")

        scope = d.pop("scope")

        environment_key = d.pop("environment_key")

        access = check_managed_postgres_binding_access(d.pop("access"))

        credential_generation = d.pop("credential_generation")

        state = check_managed_postgres_binding_state(d.pop("state"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        def _parse_last_error_code(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        last_error_code = _parse_last_error_code(d.pop("last_error_code", UNSET))

        managed_postgres_binding = cls(
            id=id,
            database_id=database_id,
            app_id=app_id,
            scope=scope,
            environment_key=environment_key,
            access=access,
            credential_generation=credential_generation,
            state=state,
            created_at=created_at,
            updated_at=updated_at,
            last_error_code=last_error_code,
        )

        managed_postgres_binding.additional_properties = d
        return managed_postgres_binding

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
