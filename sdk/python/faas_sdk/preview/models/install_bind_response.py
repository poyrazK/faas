from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.install_bind_response_deploy_branches import InstallBindResponseDeployBranches


T = TypeVar("T", bound="InstallBindResponse")


@_attrs_define
class InstallBindResponse:
    """Successful bind. `binding_id` is the deterministic `bind-<appID>-<repo>` form used in audit log entries."""

    binding_id: str
    repo_full_name: str
    production_branch: str
    deploy_branches: InstallBindResponseDeployBranches | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        binding_id = self.binding_id

        repo_full_name = self.repo_full_name

        production_branch = self.production_branch

        deploy_branches: dict[str, Any] | Unset = UNSET
        if not isinstance(self.deploy_branches, Unset):
            deploy_branches = self.deploy_branches.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "binding_id": binding_id,
                "repo_full_name": repo_full_name,
                "production_branch": production_branch,
            }
        )
        if deploy_branches is not UNSET:
            field_dict["deploy_branches"] = deploy_branches

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.install_bind_response_deploy_branches import InstallBindResponseDeployBranches

        d = dict(src_dict)
        binding_id = d.pop("binding_id")

        repo_full_name = d.pop("repo_full_name")

        production_branch = d.pop("production_branch")

        _deploy_branches = d.pop("deploy_branches", UNSET)
        deploy_branches: InstallBindResponseDeployBranches | Unset
        if isinstance(_deploy_branches, Unset):
            deploy_branches = UNSET
        else:
            deploy_branches = InstallBindResponseDeployBranches.from_dict(_deploy_branches)

        install_bind_response = cls(
            binding_id=binding_id,
            repo_full_name=repo_full_name,
            production_branch=production_branch,
            deploy_branches=deploy_branches,
        )

        install_bind_response.additional_properties = d
        return install_bind_response

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
