from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ConfigRollbackReceipt")


@_attrs_define
class ConfigRollbackReceipt:
    """Value-free evidence of a reviewed configuration rollback."""

    request_id: UUID
    app_id: UUID
    scope: str
    restored_revision: int
    config_revision: int
    changed: bool
    environment_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        app_id = str(self.app_id)

        scope = self.scope

        restored_revision = self.restored_revision

        config_revision = self.config_revision

        changed = self.changed

        environment_id: str | Unset = UNSET
        if not isinstance(self.environment_id, Unset):
            environment_id = str(self.environment_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "app_id": app_id,
                "scope": scope,
                "restored_revision": restored_revision,
                "config_revision": config_revision,
                "changed": changed,
            }
        )
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        restored_revision = d.pop("restored_revision")

        config_revision = d.pop("config_revision")

        changed = d.pop("changed")

        _environment_id = d.pop("environment_id", UNSET)
        environment_id: UUID | Unset
        if isinstance(_environment_id, Unset):
            environment_id = UNSET
        else:
            environment_id = UUID(_environment_id)

        config_rollback_receipt = cls(
            request_id=request_id,
            app_id=app_id,
            scope=scope,
            restored_revision=restored_revision,
            config_revision=config_revision,
            changed=changed,
            environment_id=environment_id,
        )

        config_rollback_receipt.additional_properties = d
        return config_rollback_receipt

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
