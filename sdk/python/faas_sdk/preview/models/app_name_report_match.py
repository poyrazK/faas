from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AppNameReportMatch")


@_attrs_define
class AppNameReportMatch:
    """An existing app whose name a blocklist entry matches."""

    slug: str
    app_id: UUID
    account_id: UUID
    status: str
    entry: str
    """The blocklist entry that matched."""
    org_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        app_id = str(self.app_id)

        account_id = str(self.account_id)

        status = self.status

        entry = self.entry

        org_id: str | Unset = UNSET
        if not isinstance(self.org_id, Unset):
            org_id = str(self.org_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "app_id": app_id,
                "account_id": account_id,
                "status": status,
                "entry": entry,
            }
        )
        if org_id is not UNSET:
            field_dict["org_id"] = org_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        slug = d.pop("slug")

        app_id = UUID(d.pop("app_id"))

        account_id = UUID(d.pop("account_id"))

        status = d.pop("status")

        entry = d.pop("entry")

        _org_id = d.pop("org_id", UNSET)
        org_id: UUID | Unset
        if isinstance(_org_id, Unset):
            org_id = UNSET
        else:
            org_id = UUID(_org_id)

        app_name_report_match = cls(
            slug=slug,
            app_id=app_id,
            account_id=account_id,
            status=status,
            entry=entry,
            org_id=org_id,
        )

        app_name_report_match.additional_properties = d
        return app_name_report_match

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
