from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_project_environment_branch_request_egress import (
    CreateProjectEnvironmentBranchRequestEgress,
    check_create_project_environment_branch_request_egress,
)
from ..models.create_project_environment_branch_request_private_networks import (
    CreateProjectEnvironmentBranchRequestPrivateNetworks,
    check_create_project_environment_branch_request_private_networks,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateProjectEnvironmentBranchRequest")


@_attrs_define
class CreateProjectEnvironmentBranchRequest:
    """A new branch of the environment named in the path."""

    slug: str
    """Slug of the new environment."""
    ttl: str | Unset = UNSET
    """Time to live, a duration such as 24h between 1h and 720h. Defaults to 168h."""
    egress: CreateProjectEnvironmentBranchRequestEgress | Unset = UNSET
    """Outbound traffic from the branch. live (the default) behaves like a stage. contained reaches only the
    branch's own platform services (service calls inside the branch, the branch's managed PostgreSQL copies and the
    platform resolver) and takes no host-mediated action; it reaches the branch's own buckets through the platform's
    object relay, and a parent with object storage is refused where the operator has not enabled the relay.
    contained is available only where the operator enabled it. simulated is contained, and the branch can seed
    rehearsal members (POST /v1/dev/sessions/{project}/seed) whose side effects are simulated and recorded."""
    private_networks: CreateProjectEnvironmentBranchRequestPrivateNetworks | Unset = UNSET
    """Whether the branch's instances join the private networks their workloads are attached to. off (the default)
    keeps them off, so a call to a private peer fails rather than reaching production. join attaches them with
    addresses of their own, never a workload's service address, so they can reach the parent's private peers; only a
    live branch may join."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        ttl = self.ttl

        egress: str | Unset = UNSET
        if not isinstance(self.egress, Unset):
            egress = self.egress

        private_networks: str | Unset = UNSET
        if not isinstance(self.private_networks, Unset):
            private_networks = self.private_networks

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
            }
        )
        if ttl is not UNSET:
            field_dict["ttl"] = ttl
        if egress is not UNSET:
            field_dict["egress"] = egress
        if private_networks is not UNSET:
            field_dict["private_networks"] = private_networks

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        slug = d.pop("slug")

        ttl = d.pop("ttl", UNSET)

        _egress = d.pop("egress", UNSET)
        egress: CreateProjectEnvironmentBranchRequestEgress | Unset
        if isinstance(_egress, Unset):
            egress = UNSET
        else:
            egress = check_create_project_environment_branch_request_egress(_egress)

        _private_networks = d.pop("private_networks", UNSET)
        private_networks: CreateProjectEnvironmentBranchRequestPrivateNetworks | Unset
        if isinstance(_private_networks, Unset):
            private_networks = UNSET
        else:
            private_networks = check_create_project_environment_branch_request_private_networks(_private_networks)

        create_project_environment_branch_request = cls(
            slug=slug,
            ttl=ttl,
            egress=egress,
            private_networks=private_networks,
        )

        create_project_environment_branch_request.additional_properties = d
        return create_project_environment_branch_request

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
