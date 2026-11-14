from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="DataPipelineTenantMembership")


@_attrs_define
class DataPipelineTenantMembership:
    """One account-owner-authorized subject and tenant pair."""

    subject: str
    tenant: str

    def to_dict(self) -> dict[str, Any]:
        subject = self.subject

        tenant = self.tenant

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subject": subject,
                "tenant": tenant,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        subject = d.pop("subject")

        tenant = d.pop("tenant")

        data_pipeline_tenant_membership = cls(
            subject=subject,
            tenant=tenant,
        )

        return data_pipeline_tenant_membership
