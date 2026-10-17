from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.config_change_receipt import ConfigChangeReceipt
    from ..models.config_secret_difference import ConfigSecretDifference


T = TypeVar("T", bound="ConfigRollbackResponse")


@_attrs_define
class ConfigRollbackResponse:
    restored_revision: int
    set_: list[str]
    deleted: list[str]
    secrets_not_restored: list[ConfigSecretDifference]
    change: ConfigChangeReceipt | Unset = UNSET
    """The config revision an env or secret write recorded and the guard watching it (ADR-1451)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        restored_revision = self.restored_revision

        set_ = self.set_

        deleted = self.deleted

        secrets_not_restored = []
        for secrets_not_restored_item_data in self.secrets_not_restored:
            secrets_not_restored_item = secrets_not_restored_item_data.to_dict()
            secrets_not_restored.append(secrets_not_restored_item)

        change: dict[str, Any] | Unset = UNSET
        if not isinstance(self.change, Unset):
            change = self.change.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "restored_revision": restored_revision,
                "set": set_,
                "deleted": deleted,
                "secrets_not_restored": secrets_not_restored,
            }
        )
        if change is not UNSET:
            field_dict["change"] = change

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.config_change_receipt import ConfigChangeReceipt
        from ..models.config_secret_difference import ConfigSecretDifference

        d = dict(src_dict)
        restored_revision = d.pop("restored_revision")

        set_ = cast(list[str], d.pop("set"))

        deleted = cast(list[str], d.pop("deleted"))

        secrets_not_restored = []
        _secrets_not_restored = d.pop("secrets_not_restored")
        for secrets_not_restored_item_data in _secrets_not_restored:
            secrets_not_restored_item = ConfigSecretDifference.from_dict(secrets_not_restored_item_data)

            secrets_not_restored.append(secrets_not_restored_item)

        _change = d.pop("change", UNSET)
        change: ConfigChangeReceipt | Unset
        if isinstance(_change, Unset):
            change = UNSET
        else:
            change = ConfigChangeReceipt.from_dict(_change)

        config_rollback_response = cls(
            restored_revision=restored_revision,
            set_=set_,
            deleted=deleted,
            secrets_not_restored=secrets_not_restored,
            change=change,
        )

        config_rollback_response.additional_properties = d
        return config_rollback_response

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
