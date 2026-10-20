from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DurableEntityScope")


@_attrs_define
class DurableEntityScope:
    """Immutable account, application, environment and optional customer identity of a logical entity."""

    account_id: UUID
    app_id: UUID
    environment_id: UUID
    namespace: str
    key: str
    tenant_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = str(self.account_id)

        app_id = str(self.app_id)

        environment_id = str(self.environment_id)

        namespace = self.namespace

        key = self.key

        tenant_id: str | Unset = UNSET
        if not isinstance(self.tenant_id, Unset):
            tenant_id = str(self.tenant_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "app_id": app_id,
                "environment_id": environment_id,
                "namespace": namespace,
                "key": key,
            }
        )
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        account_id = UUID(d.pop("account_id"))

        app_id = UUID(d.pop("app_id"))

        environment_id = UUID(d.pop("environment_id"))

        namespace = d.pop("namespace")

        key = d.pop("key")

        _tenant_id = d.pop("tenant_id", UNSET)
        tenant_id: UUID | Unset
        if isinstance(_tenant_id, Unset):
            tenant_id = UNSET
        else:
            tenant_id = UUID(_tenant_id)

        durable_entity_scope = cls(
            account_id=account_id,
            app_id=app_id,
            environment_id=environment_id,
            namespace=namespace,
            key=key,
            tenant_id=tenant_id,
        )

        durable_entity_scope.additional_properties = d
        return durable_entity_scope

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
