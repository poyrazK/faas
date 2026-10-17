from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.config_change_receipt import ConfigChangeReceipt


T = TypeVar("T", bound="AppEnvResponse")


@_attrs_define
class AppEnvResponse:
    """An env var envelope: key name + scope + timestamps. The plaintext value never appears here."""

    key: str
    scope: str
    """Env-var scope (ADR-090). Always 'default' for the pre-PR-B flat response; varies for the nested
    `env_by_scope` response."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    config_change: ConfigChangeReceipt | Unset = UNSET
    """The config revision an env or secret write recorded and the guard watching it (ADR-1451)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        scope = self.scope

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        config_change: dict[str, Any] | Unset = UNSET
        if not isinstance(self.config_change, Unset):
            config_change = self.config_change.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "scope": scope,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if config_change is not UNSET:
            field_dict["config_change"] = config_change

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.config_change_receipt import ConfigChangeReceipt

        d = dict(src_dict)
        key = d.pop("key")

        scope = d.pop("scope")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _config_change = d.pop("config_change", UNSET)
        config_change: ConfigChangeReceipt | Unset
        if isinstance(_config_change, Unset):
            config_change = UNSET
        else:
            config_change = ConfigChangeReceipt.from_dict(_config_change)

        app_env_response = cls(
            key=key,
            scope=scope,
            created_at=created_at,
            updated_at=updated_at,
            config_change=config_change,
        )

        app_env_response.additional_properties = d
        return app_env_response

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
