from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AppOperationalRollback")


@_attrs_define
class AppOperationalRollback:
    """Pending checked rollback progress with stable codes and target selection; free-form reasons and blocker diagnostics
    are omitted.

    """

    id: str
    scope: str
    status: str
    target_deployment_id: str
    current_deployment_id: str
    updated_at: datetime.datetime
    code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        scope = self.scope

        status = self.status

        target_deployment_id = self.target_deployment_id

        current_deployment_id = self.current_deployment_id

        updated_at = self.updated_at.isoformat()

        code = self.code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "scope": scope,
                "status": status,
                "target_deployment_id": target_deployment_id,
                "current_deployment_id": current_deployment_id,
                "updated_at": updated_at,
            }
        )
        if code is not UNSET:
            field_dict["code"] = code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        scope = d.pop("scope")

        status = d.pop("status")

        target_deployment_id = d.pop("target_deployment_id")

        current_deployment_id = d.pop("current_deployment_id")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        code = d.pop("code", UNSET)

        app_operational_rollback = cls(
            id=id,
            scope=scope,
            status=status,
            target_deployment_id=target_deployment_id,
            current_deployment_id=current_deployment_id,
            updated_at=updated_at,
            code=code,
        )

        app_operational_rollback.additional_properties = d
        return app_operational_rollback

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
