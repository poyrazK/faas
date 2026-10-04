from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.global_app_response_routing_policy import (
    GlobalAppResponseRoutingPolicy,
    check_global_app_response_routing_policy,
)

if TYPE_CHECKING:
    from ..models.global_app_owner_response import GlobalAppOwnerResponse
    from ..models.global_app_target_response import GlobalAppTargetResponse


T = TypeVar("T", bound="GlobalAppResponse")


@_attrs_define
class GlobalAppResponse:
    """Account or organization owned global identity. Shared organizations can register identities; regional attachment
    requires the organization grant protocol. Select a workspace using X-Active-Org or org, or an organization-bound API
    key. No runtime health or deployment claim.

    """

    owner: GlobalAppOwnerResponse
    """Immutable directory tenant root. Account roots retain the account UUID; organization roots have an
    independent UUID and survive creator departure."""
    id: UUID
    slug: str
    routing_policy: GlobalAppResponseRoutingPolicy
    created_at: datetime.datetime
    targets: list[GlobalAppTargetResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        owner = self.owner.to_dict()

        id = str(self.id)

        slug = self.slug

        routing_policy: str = self.routing_policy

        created_at = self.created_at.isoformat()

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "owner": owner,
                "id": id,
                "slug": slug,
                "routing_policy": routing_policy,
                "created_at": created_at,
                "targets": targets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.global_app_owner_response import GlobalAppOwnerResponse
        from ..models.global_app_target_response import GlobalAppTargetResponse

        d = dict(src_dict)
        owner = GlobalAppOwnerResponse.from_dict(d.pop("owner"))

        id = UUID(d.pop("id"))

        slug = d.pop("slug")

        routing_policy = check_global_app_response_routing_policy(d.pop("routing_policy"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = GlobalAppTargetResponse.from_dict(targets_item_data)

            targets.append(targets_item)

        global_app_response = cls(
            owner=owner,
            id=id,
            slug=slug,
            routing_policy=routing_policy,
            created_at=created_at,
            targets=targets,
        )

        global_app_response.additional_properties = d
        return global_app_response

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
