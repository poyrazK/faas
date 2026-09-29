from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.org_app_deployment_summary_kind import OrgAppDeploymentSummaryKind, check_org_app_deployment_summary_kind
from ..models.org_app_deployment_summary_status import (
    OrgAppDeploymentSummaryStatus,
    check_org_app_deployment_summary_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OrgAppDeploymentSummary")


@_attrs_define
class OrgAppDeploymentSummary:
    """Safe deployment status projection for an application shared in a workspace. Actor attribution is on the organization
    activity timeline; detailed deployment fields stay creator-scoped.

    """

    id: str
    kind: OrgAppDeploymentSummaryKind
    """Deployment source kind."""
    status: OrgAppDeploymentSummaryStatus
    """Current deployment lifecycle status."""
    created_at: datetime.datetime
    revision: int | Unset = UNSET
    """Per-app revision when available."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        kind: str = self.kind

        status: str = self.status

        created_at = self.created_at.isoformat()

        revision = self.revision

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "kind": kind,
                "status": status,
                "created_at": created_at,
            }
        )
        if revision is not UNSET:
            field_dict["revision"] = revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        kind = check_org_app_deployment_summary_kind(d.pop("kind"))

        status = check_org_app_deployment_summary_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        revision = d.pop("revision", UNSET)

        org_app_deployment_summary = cls(
            id=id,
            kind=kind,
            status=status,
            created_at=created_at,
            revision=revision,
        )

        org_app_deployment_summary.additional_properties = d
        return org_app_deployment_summary

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
