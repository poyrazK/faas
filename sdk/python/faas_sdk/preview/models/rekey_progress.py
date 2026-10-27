from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RekeyProgress")


@_attrs_define
class RekeyProgress:
    """Cumulative rekey walk snapshot. Returned by
    GET /v1/admin/secrets/rekey-progress and persisted to
    FAAS_REKEY_PROGRESS_FILE (default
    /var/lib/faas/rekey-progress.json) on every batch tick.
    `last_id` is the (account_id, app_id, key) cursor the walk
    will resume from on daemon restart.

    """

    total: int
    """Running count of rows observed so far. Grows as the walk paginates through (account_id, app_id, key) order."""
    rekeyed: int
    """Rows successfully unsealed under the previous identity and re-sealed under the current one."""
    skipped: int
    """Rows already sealed under the current identity (no-op). Seen-set dedupe — does NOT mean the row is
    unreadable."""
    failed: int
    """Rows where the unseal step threw. A non-zero value warrants operator action (toggle FAAS_REKEY_ENABLED and
    restart apid to retry)."""
    last_id: str | Unset = UNSET
    """Resume cursor in (account_id|app_id|key) form. Empty when the walk has just started or finished a clean
    sweep."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        total = self.total

        rekeyed = self.rekeyed

        skipped = self.skipped

        failed = self.failed

        last_id = self.last_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "total": total,
                "rekeyed": rekeyed,
                "skipped": skipped,
                "failed": failed,
            }
        )
        if last_id is not UNSET:
            field_dict["last_id"] = last_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        total = d.pop("total")

        rekeyed = d.pop("rekeyed")

        skipped = d.pop("skipped")

        failed = d.pop("failed")

        last_id = d.pop("last_id", UNSET)

        rekey_progress = cls(
            total=total,
            rekeyed=rekeyed,
            skipped=skipped,
            failed=failed,
            last_id=last_id,
        )

        rekey_progress.additional_properties = d
        return rekey_progress

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
