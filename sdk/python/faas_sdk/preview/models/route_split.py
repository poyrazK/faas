from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_split_kind import RouteSplitKind, check_route_split_kind
from ..models.route_split_phase import RouteSplitPhase, check_route_split_phase
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteSplit")


@_attrs_define
class RouteSplit:
    """An applied route split and where it is."""

    id: str
    slug: str
    target_slug: str
    """The route's app."""
    kind: RouteSplitKind
    route: str
    path_glob: str
    route_ram_mb: int
    main_ram_mb: int
    previous_ram_mb: int
    """The app's size before the split; undo restores it."""
    main_resized: bool
    edge_rule_ids: list[str]
    phase: RouteSplitPhase
    created_at: datetime.datetime
    updated_at: datetime.datetime
    method: str | Unset = UNSET
    warning: str | Unset = UNSET
    error: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        slug = self.slug

        target_slug = self.target_slug

        kind: str = self.kind

        route = self.route

        path_glob = self.path_glob

        route_ram_mb = self.route_ram_mb

        main_ram_mb = self.main_ram_mb

        previous_ram_mb = self.previous_ram_mb

        main_resized = self.main_resized

        edge_rule_ids = self.edge_rule_ids

        phase: str = self.phase

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        method = self.method

        warning = self.warning

        error = self.error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "slug": slug,
                "target_slug": target_slug,
                "kind": kind,
                "route": route,
                "path_glob": path_glob,
                "route_ram_mb": route_ram_mb,
                "main_ram_mb": main_ram_mb,
                "previous_ram_mb": previous_ram_mb,
                "main_resized": main_resized,
                "edge_rule_ids": edge_rule_ids,
                "phase": phase,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if method is not UNSET:
            field_dict["method"] = method
        if warning is not UNSET:
            field_dict["warning"] = warning
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        slug = d.pop("slug")

        target_slug = d.pop("target_slug")

        kind = check_route_split_kind(d.pop("kind"))

        route = d.pop("route")

        path_glob = d.pop("path_glob")

        route_ram_mb = d.pop("route_ram_mb")

        main_ram_mb = d.pop("main_ram_mb")

        previous_ram_mb = d.pop("previous_ram_mb")

        main_resized = d.pop("main_resized")

        edge_rule_ids = cast(list[str], d.pop("edge_rule_ids"))

        phase = check_route_split_phase(d.pop("phase"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        method = d.pop("method", UNSET)

        warning = d.pop("warning", UNSET)

        error = d.pop("error", UNSET)

        route_split = cls(
            id=id,
            slug=slug,
            target_slug=target_slug,
            kind=kind,
            route=route,
            path_glob=path_glob,
            route_ram_mb=route_ram_mb,
            main_ram_mb=main_ram_mb,
            previous_ram_mb=previous_ram_mb,
            main_resized=main_resized,
            edge_rule_ids=edge_rule_ids,
            phase=phase,
            created_at=created_at,
            updated_at=updated_at,
            method=method,
            warning=warning,
            error=error,
        )

        route_split.additional_properties = d
        return route_split

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
