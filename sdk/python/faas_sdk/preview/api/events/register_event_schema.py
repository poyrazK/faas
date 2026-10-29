from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.register_event_schema_request import RegisterEventSchemaRequest
from ...models.register_event_schema_response import RegisterEventSchemaResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: RegisterEventSchemaRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/event-schemas",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RegisterEventSchemaResponse | None:
    if response.status_code == 200:
        response_200 = RegisterEventSchemaResponse.from_dict(response.json())

        return response_200

    if response.status_code == 201:
        response_201 = RegisterEventSchemaResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RegisterEventSchemaResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: RegisterEventSchemaRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | RegisterEventSchemaResponse]:
    """Register an immutable event JSON Schema version.

     Requires deploy:write or admin. The first schema registered for a
    source/type pair makes schemaversion mandatory on future publishes.
    Repeating an identical version is safe; changing a version returns 409.

    Args:
        idempotency_key (str | Unset):
        body (RegisterEventSchemaRequest): Immutable event schema registration request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RegisterEventSchemaResponse]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    body: RegisterEventSchemaRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | RegisterEventSchemaResponse | None:
    """Register an immutable event JSON Schema version.

     Requires deploy:write or admin. The first schema registered for a
    source/type pair makes schemaversion mandatory on future publishes.
    Repeating an identical version is safe; changing a version returns 409.

    Args:
        idempotency_key (str | Unset):
        body (RegisterEventSchemaRequest): Immutable event schema registration request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RegisterEventSchemaResponse
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: RegisterEventSchemaRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | RegisterEventSchemaResponse]:
    """Register an immutable event JSON Schema version.

     Requires deploy:write or admin. The first schema registered for a
    source/type pair makes schemaversion mandatory on future publishes.
    Repeating an identical version is safe; changing a version returns 409.

    Args:
        idempotency_key (str | Unset):
        body (RegisterEventSchemaRequest): Immutable event schema registration request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RegisterEventSchemaResponse]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: RegisterEventSchemaRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | RegisterEventSchemaResponse | None:
    """Register an immutable event JSON Schema version.

     Requires deploy:write or admin. The first schema registered for a
    source/type pair makes schemaversion mandatory on future publishes.
    Repeating an identical version is safe; changing a version returns 409.

    Args:
        idempotency_key (str | Unset):
        body (RegisterEventSchemaRequest): Immutable event schema registration request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RegisterEventSchemaResponse
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
