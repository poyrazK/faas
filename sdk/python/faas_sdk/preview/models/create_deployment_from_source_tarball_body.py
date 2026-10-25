from __future__ import annotations

import json
from collections.abc import Mapping
from io import BytesIO
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from .. import types
from ..types import UNSET, File, Unset

if TYPE_CHECKING:
    from ..models.source_tarball_deploy_request import SourceTarballDeployRequest


T = TypeVar("T", bound="CreateDeploymentFromSourceTarballBody")


@_attrs_define
class CreateDeploymentFromSourceTarballBody:
    tarball: File
    sidecar: SourceTarballDeployRequest | Unset = UNSET
    """Body for the informational `sidecar` form field on POST /v1/apps/{slug}/deployments/source-tarball (issue
    #961 / Mega-A PR-1, ADR-115). The CLI is the trust root for this deploy path; apid does NOT consult
    `github_installations` and does NOT attempt a server-side git fetch. The sidecar fields are recorded on the
    build row for provenance only — the build pipeline does NOT use them to fetch upstream."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tarball = self.tarball.to_tuple()

        sidecar: dict[str, Any] | Unset = UNSET
        if not isinstance(self.sidecar, Unset):
            sidecar = self.sidecar.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tarball": tarball,
            }
        )
        if sidecar is not UNSET:
            field_dict["sidecar"] = sidecar

        return field_dict

    def to_multipart(self) -> types.RequestFiles:
        files: types.RequestFiles = []

        files.append(("tarball", self.tarball.to_tuple()))

        if not isinstance(self.sidecar, Unset):
            files.append(("sidecar", (None, json.dumps(self.sidecar.to_dict()).encode(), "application/json")))

        for prop_name, prop in self.additional_properties.items():
            files.append((prop_name, (None, str(prop).encode(), "text/plain")))

        return files

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.source_tarball_deploy_request import SourceTarballDeployRequest

        d = dict(src_dict)
        tarball = File(payload=BytesIO(d.pop("tarball")))

        _sidecar = d.pop("sidecar", UNSET)
        sidecar: SourceTarballDeployRequest | Unset
        if isinstance(_sidecar, Unset):
            sidecar = UNSET
        else:
            sidecar = SourceTarballDeployRequest.from_dict(_sidecar)

        create_deployment_from_source_tarball_body = cls(
            tarball=tarball,
            sidecar=sidecar,
        )

        create_deployment_from_source_tarball_body.additional_properties = d
        return create_deployment_from_source_tarball_body

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
