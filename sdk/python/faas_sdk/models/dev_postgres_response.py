from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.dev_postgres_response_binding_state import (
    DevPostgresResponseBindingState,
    check_dev_postgres_response_binding_state,
)
from ..models.dev_postgres_response_state import DevPostgresResponseState, check_dev_postgres_response_state

T = TypeVar("T", bound="DevPostgresResponse")


@_attrs_define
class DevPostgresResponse:
    """Safe receipt for the isolated developer database and its app binding. Credentials are injected into DATABASE_URL and
    never returned.

    """

    database_id: str
    name: str
    state: DevPostgresResponseState
    binding_id: str
    binding_state: DevPostgresResponseBindingState
    environment_key: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database_id = self.database_id

        name = self.name

        state: str = self.state

        binding_id = self.binding_id

        binding_state: str = self.binding_state

        environment_key = self.environment_key

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database_id": database_id,
                "name": name,
                "state": state,
                "binding_id": binding_id,
                "binding_state": binding_state,
                "environment_key": environment_key,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        database_id = d.pop("database_id")

        name = d.pop("name")

        state = check_dev_postgres_response_state(d.pop("state"))

        binding_id = d.pop("binding_id")

        binding_state = check_dev_postgres_response_binding_state(d.pop("binding_state"))

        environment_key = d.pop("environment_key")

        dev_postgres_response = cls(
            database_id=database_id,
            name=name,
            state=state,
            binding_id=binding_id,
            binding_state=binding_state,
            environment_key=environment_key,
        )

        dev_postgres_response.additional_properties = d
        return dev_postgres_response

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
