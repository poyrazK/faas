from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.durable_entity_validator_deployment_info_source import (
    DurableEntityValidatorDeploymentInfoSource,
    check_durable_entity_validator_deployment_info_source,
)
from ..models.durable_entity_validator_deployment_info_status import (
    DurableEntityValidatorDeploymentInfoStatus,
    check_durable_entity_validator_deployment_info_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DurableEntityValidatorDeploymentInfo")


@_attrs_define
class DurableEntityValidatorDeploymentInfo:
    """Observational validator readiness on deployment detail; not a reservation or code-purity attestation."""

    status: DurableEntityValidatorDeploymentInfoStatus
    source: DurableEntityValidatorDeploymentInfoSource | Unset = UNSET
    sha256: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        source: str | Unset = UNSET
        if not isinstance(self.source, Unset):
            source = self.source

        sha256 = self.sha256

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
            }
        )
        if source is not UNSET:
            field_dict["source"] = source
        if sha256 is not UNSET:
            field_dict["sha256"] = sha256

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_durable_entity_validator_deployment_info_status(d.pop("status"))

        _source = d.pop("source", UNSET)
        source: DurableEntityValidatorDeploymentInfoSource | Unset
        if isinstance(_source, Unset):
            source = UNSET
        else:
            source = check_durable_entity_validator_deployment_info_source(_source)

        sha256 = d.pop("sha256", UNSET)

        durable_entity_validator_deployment_info = cls(
            status=status,
            source=source,
            sha256=sha256,
        )

        return durable_entity_validator_deployment_info
