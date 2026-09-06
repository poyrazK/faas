from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.install_bind_request_deploy_branches import InstallBindRequestDeployBranches


T = TypeVar("T", bound="InstallBindRequest")


@_attrs_define
class InstallBindRequest:
    """Body for both `POST /v1/install/repos/list` and
    `POST /v1/apps/{slug}/install/bind`. Carries the
    (installation_id, repo_full_name, production_branch) tuple
    the dashboard's bind picker commits. `production_branch` is
    optional — when omitted, githubd uses the install's
    `default_branch` from `/installations/{id}`.

    """

    installation_id: int
    repo_full_name: str
    production_branch: str | Unset = UNSET
    deploy_branches: InstallBindRequestDeployBranches | Unset = UNSET
    """GitHub branch names mapped to deployment environment scopes. An empty object clears existing rules."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        installation_id = self.installation_id

        repo_full_name = self.repo_full_name

        production_branch = self.production_branch

        deploy_branches: dict[str, Any] | Unset = UNSET
        if not isinstance(self.deploy_branches, Unset):
            deploy_branches = self.deploy_branches.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "installation_id": installation_id,
                "repo_full_name": repo_full_name,
            }
        )
        if production_branch is not UNSET:
            field_dict["production_branch"] = production_branch
        if deploy_branches is not UNSET:
            field_dict["deploy_branches"] = deploy_branches

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.install_bind_request_deploy_branches import InstallBindRequestDeployBranches

        d = dict(src_dict)
        installation_id = d.pop("installation_id")

        repo_full_name = d.pop("repo_full_name")

        production_branch = d.pop("production_branch", UNSET)

        _deploy_branches = d.pop("deploy_branches", UNSET)
        deploy_branches: InstallBindRequestDeployBranches | Unset
        if isinstance(_deploy_branches, Unset):
            deploy_branches = UNSET
        else:
            deploy_branches = InstallBindRequestDeployBranches.from_dict(_deploy_branches)

        install_bind_request = cls(
            installation_id=installation_id,
            repo_full_name=repo_full_name,
            production_branch=production_branch,
            deploy_branches=deploy_branches,
        )

        install_bind_request.additional_properties = d
        return install_bind_request

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
