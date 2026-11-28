from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.deleted_app_summary_restore_blocker import (
    DeletedAppSummaryRestoreBlocker,
    check_deleted_app_summary_restore_blocker,
)

T = TypeVar("T", bound="DeletedAppSummary")


@_attrs_define
class DeletedAppSummary:
    """Account-owned retained lifecycle metadata. Eligibility never reserves quota or proves execution health."""

    account_id: UUID
    id: UUID
    slug: str
    deleted_at: datetime.datetime | None
    delete_grace_until: datetime.datetime | None
    restore_eligible: bool
    restore_blocker: DeletedAppSummaryRestoreBlocker
    evaluated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = str(self.account_id)

        id = str(self.id)

        slug = self.slug

        deleted_at: None | str
        if isinstance(self.deleted_at, datetime.datetime):
            deleted_at = self.deleted_at.isoformat()
        else:
            deleted_at = self.deleted_at

        delete_grace_until: None | str
        if isinstance(self.delete_grace_until, datetime.datetime):
            delete_grace_until = self.delete_grace_until.isoformat()
        else:
            delete_grace_until = self.delete_grace_until

        restore_eligible = self.restore_eligible

        restore_blocker: str = self.restore_blocker

        evaluated_at = self.evaluated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "id": id,
                "slug": slug,
                "deleted_at": deleted_at,
                "delete_grace_until": delete_grace_until,
                "restore_eligible": restore_eligible,
                "restore_blocker": restore_blocker,
                "evaluated_at": evaluated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        account_id = UUID(d.pop("account_id"))

        id = UUID(d.pop("id"))

        slug = d.pop("slug")

        def _parse_deleted_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                deleted_at_type_0 = datetime.datetime.fromisoformat(data)

                return deleted_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        deleted_at = _parse_deleted_at(d.pop("deleted_at"))

        def _parse_delete_grace_until(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                delete_grace_until_type_0 = datetime.datetime.fromisoformat(data)

                return delete_grace_until_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        delete_grace_until = _parse_delete_grace_until(d.pop("delete_grace_until"))

        restore_eligible = d.pop("restore_eligible")

        restore_blocker = check_deleted_app_summary_restore_blocker(d.pop("restore_blocker"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        deleted_app_summary = cls(
            account_id=account_id,
            id=id,
            slug=slug,
            deleted_at=deleted_at,
            delete_grace_until=delete_grace_until,
            restore_eligible=restore_eligible,
            restore_blocker=restore_blocker,
            evaluated_at=evaluated_at,
        )

        deleted_app_summary.additional_properties = d
        return deleted_app_summary

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
