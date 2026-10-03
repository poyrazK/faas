from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="CellAppBindingResponse")


@_attrs_define
class CellAppBindingResponse:
    """Authenticated regional identity attestation for one personally owned non-deleted app. Not deployment readiness."""

    cell_id: UUID
    region: str
    account_id: UUID
    app_id: UUID
    app_slug: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        cell_id = str(self.cell_id)

        region = self.region

        account_id = str(self.account_id)

        app_id = str(self.app_id)

        app_slug = self.app_slug

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "cell_id": cell_id,
                "region": region,
                "account_id": account_id,
                "app_id": app_id,
                "app_slug": app_slug,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        cell_id = UUID(d.pop("cell_id"))

        region = d.pop("region")

        account_id = UUID(d.pop("account_id"))

        app_id = UUID(d.pop("app_id"))

        app_slug = d.pop("app_slug")

        cell_app_binding_response = cls(
            cell_id=cell_id,
            region=region,
            account_id=account_id,
            app_id=app_id,
            app_slug=app_slug,
        )

        cell_app_binding_response.additional_properties = d
        return cell_app_binding_response

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
