from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.config_change_receipt import ConfigChangeReceipt


T = TypeVar("T", bound="EnvBatchResponse")


@_attrs_define
class EnvBatchResponse:
    """Durable value-free receipt of an atomic variable batch."""

    request_id: UUID
    app_id: UUID
    scope: str
    updated_keys: list[str]
    deleted_keys: list[str]
    config_change: ConfigChangeReceipt
    """The config revision an env or secret write recorded and the guard watching it."""
    environment_id: UUID | Unset = UNSET
    """Immutable environment identity when the original named batch was pinned."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        app_id = str(self.app_id)

        scope = self.scope

        updated_keys = self.updated_keys

        deleted_keys = self.deleted_keys

        config_change = self.config_change.to_dict()

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
                "updated_keys": updated_keys,
                "deleted_keys": deleted_keys,
                "config_change": config_change,
            }
        )
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.config_change_receipt import ConfigChangeReceipt

        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        updated_keys = cast(list[str], d.pop("updated_keys"))

        deleted_keys = cast(list[str], d.pop("deleted_keys"))

        config_change = ConfigChangeReceipt.from_dict(d.pop("config_change"))

        _environment_id = d.pop("environment_id", UNSET)
        environment_id: UUID | Unset
        if isinstance(_environment_id, Unset):
            environment_id = UNSET
        else:
            environment_id = UUID(_environment_id)

        env_batch_response = cls(
            request_id=request_id,
            app_id=app_id,
            scope=scope,
            updated_keys=updated_keys,
            deleted_keys=deleted_keys,
            config_change=config_change,
            environment_id=environment_id,
        )

        env_batch_response.additional_properties = d
        return env_batch_response

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
