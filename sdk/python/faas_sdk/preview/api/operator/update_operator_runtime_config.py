from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operator_runtime_config import OperatorRuntimeConfig
from ...models.operator_runtime_config_operation import OperatorRuntimeConfigOperation
from ...models.problem import Problem
from ...models.update_operator_runtime_config_body import UpdateOperatorRuntimeConfigBody
from ...types import Response


def _get_kwargs(
    key: str,
    *,
    body: UpdateOperatorRuntimeConfigBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/admin/config/{key}".format(
            key=quote(str(key), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperatorRuntimeConfig | OperatorRuntimeConfigOperation | Problem | None:
    if response.status_code == 200:
        response_200 = OperatorRuntimeConfig.from_dict(response.json())

        return response_200

    if response.status_code == 202:
        response_202 = OperatorRuntimeConfigOperation.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OperatorRuntimeConfig | OperatorRuntimeConfigOperation | Problem]:
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
    body: UpdateOperatorRuntimeConfigBody,
) -> Response[OperatorRuntimeConfig | OperatorRuntimeConfigOperation | Problem]:
    """Update an operator runtime setting

     Updates a catalogued setting without an SSH session. Hot settings are
    applied immediately; graceful settings return a durable asynchronous
    operation. The write is versioned, audited, persisted in PostgreSQL,
    and propagated over pg_notify. Bootstrap, rolling, and break-glass
    settings remain deployment-managed until their corresponding
    controller is available.

    Args:
        key (str):
        body (UpdateOperatorRuntimeConfigBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperatorRuntimeConfig | OperatorRuntimeConfigOperation | Problem]
    """

    kwargs = _get_kwargs(
        key=key,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateOperatorRuntimeConfigBody,
) -> OperatorRuntimeConfig | OperatorRuntimeConfigOperation | Problem | None:
    """Update an operator runtime setting

     Updates a catalogued setting without an SSH session. Hot settings are
    applied immediately; graceful settings return a durable asynchronous
    operation. The write is versioned, audited, persisted in PostgreSQL,
    and propagated over pg_notify. Bootstrap, rolling, and break-glass
    settings remain deployment-managed until their corresponding
    controller is available.

    Args:
        key (str):
        body (UpdateOperatorRuntimeConfigBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperatorRuntimeConfig | OperatorRuntimeConfigOperation | Problem
    """

    return sync_detailed(
        key=key,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateOperatorRuntimeConfigBody,
) -> Response[OperatorRuntimeConfig | OperatorRuntimeConfigOperation | Problem]:
    """Update an operator runtime setting

     Updates a catalogued setting without an SSH session. Hot settings are
    applied immediately; graceful settings return a durable asynchronous
    operation. The write is versioned, audited, persisted in PostgreSQL,
    and propagated over pg_notify. Bootstrap, rolling, and break-glass
    settings remain deployment-managed until their corresponding
    controller is available.

    Args:
        key (str):
        body (UpdateOperatorRuntimeConfigBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperatorRuntimeConfig | OperatorRuntimeConfigOperation | Problem]
    """

    kwargs = _get_kwargs(
        key=key,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateOperatorRuntimeConfigBody,
) -> OperatorRuntimeConfig | OperatorRuntimeConfigOperation | Problem | None:
    """Update an operator runtime setting

     Updates a catalogued setting without an SSH session. Hot settings are
    applied immediately; graceful settings return a durable asynchronous
    operation. The write is versioned, audited, persisted in PostgreSQL,
    and propagated over pg_notify. Bootstrap, rolling, and break-glass
    settings remain deployment-managed until their corresponding
    controller is available.

    Args:
        key (str):
        body (UpdateOperatorRuntimeConfigBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperatorRuntimeConfig | OperatorRuntimeConfigOperation | Problem
    """

    return (
        await asyncio_detailed(
            key=key,
            client=client,
            body=body,
        )
    ).parsed
