from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.edge_rule_validate_parameters_headers import EdgeRuleValidateParametersHeaders
    from ..models.edge_rule_validate_parameters_path import EdgeRuleValidateParametersPath
    from ..models.edge_rule_validate_parameters_query import EdgeRuleValidateParametersQuery


T = TypeVar("T", bound="EdgeRuleValidateParameters")


@_attrs_define
class EdgeRuleValidateParameters:
    """Path, query, and header validation for a kind=validate rule
    (ADR-091 amendment: request parameters). Each schema is a JSON
    Schema object whose properties are parameter names; property types
    must be string, integer, number, boolean, or an array of those.
    Request values are converted to the declared type before
    validation, and a value that does not convert stays a string so
    the schema reports it. All three are checked before the body is
    read, in the rule's validate_mode; errors name the location
    (`path/id`, `query/limit`, `headers/x-tenant`).

    """

    path_template: str | Unset = UNSET
    """OpenAPI path template, such as `/users/{id}`. Required with
    `path`. The rule's `match_path` must equal the template with
    each placeholder replaced by `?*` (`/users/?*`), and each
    segment may hold at most one placeholder.
    """
    path: EdgeRuleValidateParametersPath | Unset = UNSET
    """Object schema whose properties are exactly the template's placeholders."""
    query: EdgeRuleValidateParametersQuery | Unset = UNSET
    """Object schema over every query parameter; a repeated name
    becomes an array, and `additionalProperties: false` rejects
    unknown parameters.
    """
    headers: EdgeRuleValidateParametersHeaders | Unset = UNSET
    """Object schema over the headers it declares, named in lowercase.
    Comma-separated values become an array for array properties.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        path_template = self.path_template

        path: dict[str, Any] | Unset = UNSET
        if not isinstance(self.path, Unset):
            path = self.path.to_dict()

        query: dict[str, Any] | Unset = UNSET
        if not isinstance(self.query, Unset):
            query = self.query.to_dict()

        headers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.headers, Unset):
            headers = self.headers.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if path_template is not UNSET:
            field_dict["path_template"] = path_template
        if path is not UNSET:
            field_dict["path"] = path
        if query is not UNSET:
            field_dict["query"] = query
        if headers is not UNSET:
            field_dict["headers"] = headers

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_validate_parameters_headers import EdgeRuleValidateParametersHeaders
        from ..models.edge_rule_validate_parameters_path import EdgeRuleValidateParametersPath
        from ..models.edge_rule_validate_parameters_query import EdgeRuleValidateParametersQuery

        d = dict(src_dict)
        path_template = d.pop("path_template", UNSET)

        _path = d.pop("path", UNSET)
        path: EdgeRuleValidateParametersPath | Unset
        if isinstance(_path, Unset):
            path = UNSET
        else:
            path = EdgeRuleValidateParametersPath.from_dict(_path)

        _query = d.pop("query", UNSET)
        query: EdgeRuleValidateParametersQuery | Unset
        if isinstance(_query, Unset):
            query = UNSET
        else:
            query = EdgeRuleValidateParametersQuery.from_dict(_query)

        _headers = d.pop("headers", UNSET)
        headers: EdgeRuleValidateParametersHeaders | Unset
        if isinstance(_headers, Unset):
            headers = UNSET
        else:
            headers = EdgeRuleValidateParametersHeaders.from_dict(_headers)

        edge_rule_validate_parameters = cls(
            path_template=path_template,
            path=path,
            query=query,
            headers=headers,
        )

        edge_rule_validate_parameters.additional_properties = d
        return edge_rule_validate_parameters

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
