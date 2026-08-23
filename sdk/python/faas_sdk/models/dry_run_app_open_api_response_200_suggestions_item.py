from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.dry_run_app_open_api_response_200_suggestions_item_kind import (
    DryRunAppOpenAPIResponse200SuggestionsItemKind,
    check_dry_run_app_open_api_response_200_suggestions_item_kind,
)
from ..models.dry_run_app_open_api_response_200_suggestions_item_methods_item import (
    DryRunAppOpenAPIResponse200SuggestionsItemMethodsItem,
    check_dry_run_app_open_api_response_200_suggestions_item_methods_item,
)

if TYPE_CHECKING:
    from ..models.dry_run_app_open_api_response_200_suggestions_item_action import (
        DryRunAppOpenAPIResponse200SuggestionsItemAction,
    )


T = TypeVar("T", bound="DryRunAppOpenAPIResponse200SuggestionsItem")


@_attrs_define
class DryRunAppOpenAPIResponse200SuggestionsItem:
    path: str
    methods: list[DryRunAppOpenAPIResponse200SuggestionsItemMethodsItem]
    kind: DryRunAppOpenAPIResponse200SuggestionsItemKind
    action: DryRunAppOpenAPIResponse200SuggestionsItemAction
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        methods = []
        for methods_item_data in self.methods:
            methods_item: str = methods_item_data
            methods.append(methods_item)

        kind: str = self.kind

        action = self.action.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "path": path,
                "methods": methods,
                "kind": kind,
                "action": action,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.dry_run_app_open_api_response_200_suggestions_item_action import (
            DryRunAppOpenAPIResponse200SuggestionsItemAction,
        )

        d = dict(src_dict)
        path = d.pop("path")

        methods = []
        _methods = d.pop("methods")
        for methods_item_data in _methods:
            methods_item = check_dry_run_app_open_api_response_200_suggestions_item_methods_item(methods_item_data)

            methods.append(methods_item)

        kind = check_dry_run_app_open_api_response_200_suggestions_item_kind(d.pop("kind"))

        action = DryRunAppOpenAPIResponse200SuggestionsItemAction.from_dict(d.pop("action"))

        dry_run_app_open_api_response_200_suggestions_item = cls(
            path=path,
            methods=methods,
            kind=kind,
            action=action,
        )

        dry_run_app_open_api_response_200_suggestions_item.additional_properties = d
        return dry_run_app_open_api_response_200_suggestions_item

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
