from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_scaling_receipt import AppScalingReceipt
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    request_id: UUID,
    *,
    environment: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/scaling/requests/{request_id}".format(
            slug=quote(str(slug), safe=""),
            request_id=quote(str(request_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppScalingReceipt | Problem | None:
    if response.status_code == 200:
        response_200 = AppScalingReceipt.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[AppScalingReceipt | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    request_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    environment: str | Unset = UNSET,
) -> Response[AppScalingReceipt | Problem]:
    """Recover a scaling configuration receipt without performing a write.

     Requires current read access to the app and selected environment. Receipt settings may have been
    superseded; read the current revision separately.

    Args:
        slug (str):
        request_id (UUID):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppScalingReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        request_id=request_id,
        environment=environment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    request_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    environment: str | Unset = UNSET,
) -> AppScalingReceipt | Problem | None:
    """Recover a scaling configuration receipt without performing a write.

     Requires current read access to the app and selected environment. Receipt settings may have been
    superseded; read the current revision separately.

    Args:
        slug (str):
        request_id (UUID):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppScalingReceipt | Problem
    """

    return sync_detailed(
        slug=slug,
        request_id=request_id,
        client=client,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    request_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    environment: str | Unset = UNSET,
) -> Response[AppScalingReceipt | Problem]:
    """Recover a scaling configuration receipt without performing a write.

     Requires current read access to the app and selected environment. Receipt settings may have been
    superseded; read the current revision separately.

    Args:
        slug (str):
        request_id (UUID):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppScalingReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        request_id=request_id,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    request_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    environment: str | Unset = UNSET,
) -> AppScalingReceipt | Problem | None:
    """Recover a scaling configuration receipt without performing a write.

     Requires current read access to the app and selected environment. Receipt settings may have been
    superseded; read the current revision separately.

    Args:
        slug (str):
        request_id (UUID):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppScalingReceipt | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            request_id=request_id,
            client=client,
            environment=environment,
        )
    ).parsed
