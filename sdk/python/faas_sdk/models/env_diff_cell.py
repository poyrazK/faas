from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EnvDiffCell")


@_attrs_define
class EnvDiffCell:
    """One (scope, row) cell in the env-diff matrix. The shape is
    closed and uniform across row kinds; the difference is which
    optional fields are populated. Security: secret cells never
    emit a `value` field; env cells never emit a `value_hash`
    field. Pre-PR-C rows have value_hash = '' and emit no field.

    """

    present: bool
    """True if the (row.key, scope) pair is stamped on the app; false if missing."""
    value_hash: str | Unset = UNSET
    """16-hex HMAC-SHA256(plaintext) keyed by the per-host host.hmac.key. Secret cells only."""
    value: str | Unset = UNSET
    """Plaintext env var. Env cells only; NEVER populated on secret cells (the load-bearing security property of
    the endpoint)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        present = self.present

        value_hash = self.value_hash

        value = self.value

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "present": present,
            }
        )
        if value_hash is not UNSET:
            field_dict["value_hash"] = value_hash
        if value is not UNSET:
            field_dict["value"] = value

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        present = d.pop("present")

        value_hash = d.pop("value_hash", UNSET)

        value = d.pop("value", UNSET)

        env_diff_cell = cls(
            present=present,
            value_hash=value_hash,
            value=value,
        )

        env_diff_cell.additional_properties = d
        return env_diff_cell

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
