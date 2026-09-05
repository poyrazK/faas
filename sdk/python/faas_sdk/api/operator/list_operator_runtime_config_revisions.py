from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_operator_runtime_config_revisions_response_200 import ListOperatorRuntimeConfigRevisionsResponse200
from ...models.list_operator_runtime_config_revisions_scope import (
    ListOperatorRuntimeConfigRevisionsScope,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    key: str,
    *,
    limit: int | Unset = 50,
    scope: ListOperatorRuntimeConfigRevisionsScope | Unset = "global",
    scope_id: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    json_scope: str | Unset = UNSET
    if not isinstance(scope, Unset):
        json_scope = scope

    params["scope"] = json_scope

    params["scope_id"] = scope_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/config/{key}/revisions".format(
            key=quote(str(key), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListOperatorRuntimeConfigRevisionsResponse200 | Problem | None:
    if response.status_code == 200:
        response_200 = ListOperatorRuntimeConfigRevisionsResponse200.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ListOperatorRuntimeConfigRevisionsResponse200 | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    key: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
    scope: ListOperatorRuntimeConfigRevisionsScope | Unset = "global",
    scope_id: str | Unset = UNSET,
) -> Response[ListOperatorRuntimeConfigRevisionsResponse200 | Problem]:
    """List runtime configuration revisions

     Read-only append-only version history for one catalogued setting.

    Args:
        key (str):
        limit (int | Unset):  Default: 50.
        scope (ListOperatorRuntimeConfigRevisionsScope | Unset):  Default: 'global'.
        scope_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListOperatorRuntimeConfigRevisionsResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        key=key,
        limit=limit,
        scope=scope,
        scope_id=scope_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    key: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
    scope: ListOperatorRuntimeConfigRevisionsScope | Unset = "global",
    scope_id: str | Unset = UNSET,
) -> ListOperatorRuntimeConfigRevisionsResponse200 | Problem | None:
    """List runtime configuration revisions

     Read-only append-only version history for one catalogued setting.

    Args:
        key (str):
        limit (int | Unset):  Default: 50.
        scope (ListOperatorRuntimeConfigRevisionsScope | Unset):  Default: 'global'.
        scope_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListOperatorRuntimeConfigRevisionsResponse200 | Problem
    """

    return sync_detailed(
        key=key,
        client=client,
        limit=limit,
        scope=scope,
        scope_id=scope_id,
    ).parsed


async def asyncio_detailed(
    key: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
    scope: ListOperatorRuntimeConfigRevisionsScope | Unset = "global",
    scope_id: str | Unset = UNSET,
) -> Response[ListOperatorRuntimeConfigRevisionsResponse200 | Problem]:
    """List runtime configuration revisions

     Read-only append-only version history for one catalogued setting.

    Args:
        key (str):
        limit (int | Unset):  Default: 50.
        scope (ListOperatorRuntimeConfigRevisionsScope | Unset):  Default: 'global'.
        scope_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListOperatorRuntimeConfigRevisionsResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        key=key,
        limit=limit,
        scope=scope,
        scope_id=scope_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    key: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
    scope: ListOperatorRuntimeConfigRevisionsScope | Unset = "global",
    scope_id: str | Unset = UNSET,
) -> ListOperatorRuntimeConfigRevisionsResponse200 | Problem | None:
    """List runtime configuration revisions

     Read-only append-only version history for one catalogued setting.

    Args:
        key (str):
        limit (int | Unset):  Default: 50.
        scope (ListOperatorRuntimeConfigRevisionsScope | Unset):  Default: 'global'.
        scope_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListOperatorRuntimeConfigRevisionsResponse200 | Problem
    """

    return (
        await asyncio_detailed(
            key=key,
            client=client,
            limit=limit,
            scope=scope,
            scope_id=scope_id,
        )
    ).parsed
