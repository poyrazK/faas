from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.org_app_summary_type import OrgAppSummaryType, check_org_app_summary_type
from ..types import UNSET, Unset

T = TypeVar("T", bound="OrgAppSummary")


@_attrs_define
class OrgAppSummary:
    """Deliberately small inventory projection for an organization app.
    Does not expose creator identity, environment, secrets, or app
    configuration while app-specific routes remain creator-scoped.

    """

    id: UUID
    slug: str
    type_: OrgAppSummaryType
    status: str
    created_at: datetime.datetime
    runtime: str | Unset = UNSET
    """Runtime identifier for functions when configured."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        slug = self.slug

        type_: str = self.type_

        status = self.status

        created_at = self.created_at.isoformat()

        runtime = self.runtime

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "slug": slug,
                "type": type_,
                "status": status,
                "created_at": created_at,
            }
        )
        if runtime is not UNSET:
            field_dict["runtime"] = runtime

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        slug = d.pop("slug")

        type_ = check_org_app_summary_type(d.pop("type"))

        status = d.pop("status")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        runtime = d.pop("runtime", UNSET)

        org_app_summary = cls(
            id=id,
            slug=slug,
            type_=type_,
            status=status,
            created_at=created_at,
            runtime=runtime,
        )

        org_app_summary.additional_properties = d
        return org_app_summary

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
