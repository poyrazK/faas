from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.after_restore_hook_identity import AfterRestoreHookIdentity, check_after_restore_hook_identity
from ..types import UNSET, Unset

T = TypeVar("T", bound="AfterRestoreHook")


@_attrs_define
class AfterRestoreHook:
    """Optional loopback callback that must succeed after snapshot restore, or after a frozen instance resumes, before the
    instance becomes ready.

    """

    path: str | Unset = UNSET
    """Absolute path on the app's loopback HTTP listener. Called with POST after restore, before readiness."""
    timeout_ms: int | Unset = 500
    """Callback timeout in milliseconds; 0 uses the 500 ms default."""
    identity: AfterRestoreHookIdentity | Unset = UNSET
    """reload lets an environment branch wake from the parent deployment's credential-free capture instead of a
    cold boot; the callback then re-reads config and reconnects under the branch's identity. Omit to cold-boot
    branches."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        timeout_ms = self.timeout_ms

        identity: str | Unset = UNSET
        if not isinstance(self.identity, Unset):
            identity = self.identity

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if path is not UNSET:
            field_dict["path"] = path
        if timeout_ms is not UNSET:
            field_dict["timeout_ms"] = timeout_ms
        if identity is not UNSET:
            field_dict["identity"] = identity

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        path = d.pop("path", UNSET)

        timeout_ms = d.pop("timeout_ms", UNSET)

        _identity = d.pop("identity", UNSET)
        identity: AfterRestoreHookIdentity | Unset
        if isinstance(_identity, Unset):
            identity = UNSET
        else:
            identity = check_after_restore_hook_identity(_identity)

        after_restore_hook = cls(
            path=path,
            timeout_ms=timeout_ms,
            identity=identity,
        )

        after_restore_hook.additional_properties = d
        return after_restore_hook

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
