from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.tool_publication_readiness_role import ToolPublicationReadinessRole


T = TypeVar("T", bound="ToolPublicationReadiness")


@_attrs_define
class ToolPublicationReadiness:
    """Reviewed default-environment role expectations without credential sources or monitors."""

    policy_revision: UUID
    endpoint: str
    max_age_seconds: int
    roles: list[ToolPublicationReadinessRole]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        policy_revision = str(self.policy_revision)

        endpoint = self.endpoint

        max_age_seconds = self.max_age_seconds

        roles = []
        for roles_item_data in self.roles:
            roles_item = roles_item_data.to_dict()
            roles.append(roles_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "policy_revision": policy_revision,
                "endpoint": endpoint,
                "max_age_seconds": max_age_seconds,
                "roles": roles,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tool_publication_readiness_role import ToolPublicationReadinessRole

        d = dict(src_dict)
        policy_revision = UUID(d.pop("policy_revision"))

        endpoint = d.pop("endpoint")

        max_age_seconds = d.pop("max_age_seconds")

        roles = []
        _roles = d.pop("roles")
        for roles_item_data in _roles:
            roles_item = ToolPublicationReadinessRole.from_dict(roles_item_data)

            roles.append(roles_item)

        tool_publication_readiness = cls(
            policy_revision=policy_revision,
            endpoint=endpoint,
            max_age_seconds=max_age_seconds,
            roles=roles,
        )

        tool_publication_readiness.additional_properties = d
        return tool_publication_readiness

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
