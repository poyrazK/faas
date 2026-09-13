from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.org_with_role import OrgWithRole


T = TypeVar("T", bound="OrgMeResponse")


@_attrs_define
class OrgMeResponse:
    """GET /v1/orgs/me response. Without an X-Active-Org / ?org= hint,
    `org` is the caller's personal organization. It is null only for a
    legacy account that predates the personal-org backfill.

    """

    org: OrgWithRole
    """OrgResponse + the caller's role on the active org. Used by
    GET /v1/orgs/me (`OrgMeResponse.org`). The role field is
    a closed enum: owner|admin|developer|viewer|billing.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        org = self.org.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "org": org,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.org_with_role import OrgWithRole

        d = dict(src_dict)
        org = OrgWithRole.from_dict(d.pop("org"))

        org_me_response = cls(
            org=org,
        )

        org_me_response.additional_properties = d
        return org_me_response

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
