from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operator_intent_response import OperatorIntentResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/operator-intents/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperatorIntentResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperatorIntentResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OperatorIntentResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OperatorIntentResponse | Problem]:
    r"""Read the current state of an operator intent (admin-only).

     Returns the row written by the 202 Accepted response of
    POST /v1/admin/instances/{id}/force-park, POST
    /v1/admin/instances/{id}/force-restart, or POST
    /v1/admin/apps/{slug}/force-cold-boot. Status is one of
    \"pending\" | \"running\" | \"succeeded\" | \"failed\" |
    \"cancelled\". SnapIDsMarkedStale is populated on terminal
    status for force_cold_boot and force_restart intents
    (warm + init tiers walked). On failure, Error carries
    the bounded dispatch error message (1 KB cap).

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperatorIntentResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> OperatorIntentResponse | Problem | None:
    r"""Read the current state of an operator intent (admin-only).

     Returns the row written by the 202 Accepted response of
    POST /v1/admin/instances/{id}/force-park, POST
    /v1/admin/instances/{id}/force-restart, or POST
    /v1/admin/apps/{slug}/force-cold-boot. Status is one of
    \"pending\" | \"running\" | \"succeeded\" | \"failed\" |
    \"cancelled\". SnapIDsMarkedStale is populated on terminal
    status for force_cold_boot and force_restart intents
    (warm + init tiers walked). On failure, Error carries
    the bounded dispatch error message (1 KB cap).

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperatorIntentResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OperatorIntentResponse | Problem]:
    r"""Read the current state of an operator intent (admin-only).

     Returns the row written by the 202 Accepted response of
    POST /v1/admin/instances/{id}/force-park, POST
    /v1/admin/instances/{id}/force-restart, or POST
    /v1/admin/apps/{slug}/force-cold-boot. Status is one of
    \"pending\" | \"running\" | \"succeeded\" | \"failed\" |
    \"cancelled\". SnapIDsMarkedStale is populated on terminal
    status for force_cold_boot and force_restart intents
    (warm + init tiers walked). On failure, Error carries
    the bounded dispatch error message (1 KB cap).

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperatorIntentResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> OperatorIntentResponse | Problem | None:
    r"""Read the current state of an operator intent (admin-only).

     Returns the row written by the 202 Accepted response of
    POST /v1/admin/instances/{id}/force-park, POST
    /v1/admin/instances/{id}/force-restart, or POST
    /v1/admin/apps/{slug}/force-cold-boot. Status is one of
    \"pending\" | \"running\" | \"succeeded\" | \"failed\" |
    \"cancelled\". SnapIDsMarkedStale is populated on terminal
    status for force_cold_boot and force_restart intents
    (warm + init tiers walked). On failure, Error carries
    the bounded dispatch error message (1 KB cap).

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperatorIntentResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
