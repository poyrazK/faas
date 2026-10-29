from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_self_deployment_response_status import (
    PlatformTenantSelfDeploymentResponseStatus,
    check_platform_tenant_self_deployment_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformTenantSelfDeploymentResponse")


@_attrs_define
class PlatformTenantSelfDeploymentResponse:
    """Safe status of the latest deployment attempt for the surface's app. This is not a claim that the attempt is
    currently serving; IDs, source metadata, logs, and errors are omitted.

    """

    status: PlatformTenantSelfDeploymentResponseStatus
    started_at: datetime.datetime
    revision: int | Unset = UNSET
    """Positive app revision when assigned."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        started_at = self.started_at.isoformat()

        revision = self.revision

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "started_at": started_at,
            }
        )
        if revision is not UNSET:
            field_dict["revision"] = revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_platform_tenant_self_deployment_response_status(d.pop("status"))

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        revision = d.pop("revision", UNSET)

        platform_tenant_self_deployment_response = cls(
            status=status,
            started_at=started_at,
            revision=revision,
        )

        platform_tenant_self_deployment_response.additional_properties = d
        return platform_tenant_self_deployment_response

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
