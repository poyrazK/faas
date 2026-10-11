from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.route_lifecycle_mapping_method import RouteLifecycleMappingMethod, check_route_lifecycle_mapping_method
from ..models.route_lifecycle_mapping_successor_method import (
    RouteLifecycleMappingSuccessorMethod,
    check_route_lifecycle_mapping_successor_method,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteLifecycleMapping")


@_attrs_define
class RouteLifecycleMapping:
    """Retiring operation mapped to a verified captured successor."""

    method: RouteLifecycleMappingMethod
    path: str
    successor_url: str
    """Exact candidate x-gregale-successor HTTPS URL on the successor app's canonical host or verified application-
    wide custom domain, with no port, query or fragment. Ambiguous routing and tenant surfaces require further
    review."""
    successor_method: RouteLifecycleMappingSuccessorMethod
    successor_path: str
    successor_app_id: UUID | Unset = UNSET
    """Same-account destination app. Supply together with successor_deployment_id and successor_contract_sha256;
    omit all three for the source app candidate."""
    successor_deployment_id: UUID | Unset = UNSET
    """Destination deployment. Legacy apps require one production destination at 100 percent. Project apps require
    one live active production graph member with frozen settings. Source-app successors must use the candidate."""
    successor_contract_sha256: str | Unset = UNSET
    """Authoritative destination doc_sha256 capture metadata."""

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        path = self.path

        successor_url = self.successor_url

        successor_method: str = self.successor_method

        successor_path = self.successor_path

        successor_app_id: str | Unset = UNSET
        if not isinstance(self.successor_app_id, Unset):
            successor_app_id = str(self.successor_app_id)

        successor_deployment_id: str | Unset = UNSET
        if not isinstance(self.successor_deployment_id, Unset):
            successor_deployment_id = str(self.successor_deployment_id)

        successor_contract_sha256 = self.successor_contract_sha256

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "method": method,
                "path": path,
                "successor_url": successor_url,
                "successor_method": successor_method,
                "successor_path": successor_path,
            }
        )
        if successor_app_id is not UNSET:
            field_dict["successor_app_id"] = successor_app_id
        if successor_deployment_id is not UNSET:
            field_dict["successor_deployment_id"] = successor_deployment_id
        if successor_contract_sha256 is not UNSET:
            field_dict["successor_contract_sha256"] = successor_contract_sha256

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        method = check_route_lifecycle_mapping_method(d.pop("method"))

        path = d.pop("path")

        successor_url = d.pop("successor_url")

        successor_method = check_route_lifecycle_mapping_successor_method(d.pop("successor_method"))

        successor_path = d.pop("successor_path")

        _successor_app_id = d.pop("successor_app_id", UNSET)
        successor_app_id: UUID | Unset
        if isinstance(_successor_app_id, Unset):
            successor_app_id = UNSET
        else:
            successor_app_id = UUID(_successor_app_id)

        _successor_deployment_id = d.pop("successor_deployment_id", UNSET)
        successor_deployment_id: UUID | Unset
        if isinstance(_successor_deployment_id, Unset):
            successor_deployment_id = UNSET
        else:
            successor_deployment_id = UUID(_successor_deployment_id)

        successor_contract_sha256 = d.pop("successor_contract_sha256", UNSET)

        route_lifecycle_mapping = cls(
            method=method,
            path=path,
            successor_url=successor_url,
            successor_method=successor_method,
            successor_path=successor_path,
            successor_app_id=successor_app_id,
            successor_deployment_id=successor_deployment_id,
            successor_contract_sha256=successor_contract_sha256,
        )

        return route_lifecycle_mapping
