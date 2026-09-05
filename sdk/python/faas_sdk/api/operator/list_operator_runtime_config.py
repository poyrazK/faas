from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_operator_runtime_config_response_200 import ListOperatorRuntimeConfigResponse200
from ...models.list_operator_runtime_config_scope import (
    ListOperatorRuntimeConfigScope,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    scope: ListOperatorRuntimeConfigScope | Unset = "global",
    scope_id: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_scope: str | Unset = UNSET
    if not isinstance(scope, Unset):
        json_scope = scope

    params["scope"] = json_scope

    params["scope_id"] = scope_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/config",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListOperatorRuntimeConfigResponse200 | Problem | None:
    if response.status_code == 200:
        response_200 = ListOperatorRuntimeConfigResponse200.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ListOperatorRuntimeConfigResponse200 | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    scope: ListOperatorRuntimeConfigScope | Unset = "global",
    scope_id: str | Unset = UNSET,
) -> Response[ListOperatorRuntimeConfigResponse200 | Problem]:
    """List operator runtime configuration

     Returns the closed configuration catalog together with desired and
    effective values. Sensitive bootstrap settings are redacted. This is
    an operator-only route and is not part of the customer API.

    Args:
        scope (ListOperatorRuntimeConfigScope | Unset):  Default: 'global'.
        scope_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListOperatorRuntimeConfigResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        scope=scope,
        scope_id=scope_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    scope: ListOperatorRuntimeConfigScope | Unset = "global",
    scope_id: str | Unset = UNSET,
) -> ListOperatorRuntimeConfigResponse200 | Problem | None:
    """List operator runtime configuration

     Returns the closed configuration catalog together with desired and
    effective values. Sensitive bootstrap settings are redacted. This is
    an operator-only route and is not part of the customer API.

    Args:
        scope (ListOperatorRuntimeConfigScope | Unset):  Default: 'global'.
        scope_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListOperatorRuntimeConfigResponse200 | Problem
    """

    return sync_detailed(
        client=client,
        scope=scope,
        scope_id=scope_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    scope: ListOperatorRuntimeConfigScope | Unset = "global",
    scope_id: str | Unset = UNSET,
) -> Response[ListOperatorRuntimeConfigResponse200 | Problem]:
    """List operator runtime configuration

     Returns the closed configuration catalog together with desired and
    effective values. Sensitive bootstrap settings are redacted. This is
    an operator-only route and is not part of the customer API.

    Args:
        scope (ListOperatorRuntimeConfigScope | Unset):  Default: 'global'.
        scope_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListOperatorRuntimeConfigResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        scope=scope,
        scope_id=scope_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    scope: ListOperatorRuntimeConfigScope | Unset = "global",
    scope_id: str | Unset = UNSET,
) -> ListOperatorRuntimeConfigResponse200 | Problem | None:
    """List operator runtime configuration

     Returns the closed configuration catalog together with desired and
    effective values. Sensitive bootstrap settings are redacted. This is
    an operator-only route and is not part of the customer API.

    Args:
        scope (ListOperatorRuntimeConfigScope | Unset):  Default: 'global'.
        scope_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListOperatorRuntimeConfigResponse200 | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            scope=scope,
            scope_id=scope_id,
        )
    ).parsed
