from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.config_rollback_receipt import ConfigRollbackReceipt
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    request_id: UUID,
    *,
    scope: str | Unset = UNSET,
    x_gregale_env_receipt_proof: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["X-Gregale-Env-Receipt-Proof"] = x_gregale_env_receipt_proof

    params: dict[str, Any] = {}

    params["scope"] = scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/config/rollback/requests/{request_id}".format(
            slug=quote(str(slug), safe=""),
            request_id=quote(str(request_id), safe=""),
        ),
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ConfigRollbackReceipt | Problem | None:
    if response.status_code == 200:
        response_200 = ConfigRollbackReceipt.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ConfigRollbackReceipt | Problem]:
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
    scope: str | Unset = UNSET,
    x_gregale_env_receipt_proof: str,
) -> Response[ConfigRollbackReceipt | Problem]:
    """Read a reviewed configuration rollback receipt.

     Returns value-free evidence for the original app and environment, subject to current read access.
    Never replays a rollback.

    Args:
        slug (str):
        request_id (UUID):
        scope (str | Unset):
        x_gregale_env_receipt_proof (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConfigRollbackReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        request_id=request_id,
        scope=scope,
        x_gregale_env_receipt_proof=x_gregale_env_receipt_proof,
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
    scope: str | Unset = UNSET,
    x_gregale_env_receipt_proof: str,
) -> ConfigRollbackReceipt | Problem | None:
    """Read a reviewed configuration rollback receipt.

     Returns value-free evidence for the original app and environment, subject to current read access.
    Never replays a rollback.

    Args:
        slug (str):
        request_id (UUID):
        scope (str | Unset):
        x_gregale_env_receipt_proof (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConfigRollbackReceipt | Problem
    """

    return sync_detailed(
        slug=slug,
        request_id=request_id,
        client=client,
        scope=scope,
        x_gregale_env_receipt_proof=x_gregale_env_receipt_proof,
    ).parsed


async def asyncio_detailed(
    slug: str,
    request_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
    x_gregale_env_receipt_proof: str,
) -> Response[ConfigRollbackReceipt | Problem]:
    """Read a reviewed configuration rollback receipt.

     Returns value-free evidence for the original app and environment, subject to current read access.
    Never replays a rollback.

    Args:
        slug (str):
        request_id (UUID):
        scope (str | Unset):
        x_gregale_env_receipt_proof (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConfigRollbackReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        request_id=request_id,
        scope=scope,
        x_gregale_env_receipt_proof=x_gregale_env_receipt_proof,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    request_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
    x_gregale_env_receipt_proof: str,
) -> ConfigRollbackReceipt | Problem | None:
    """Read a reviewed configuration rollback receipt.

     Returns value-free evidence for the original app and environment, subject to current read access.
    Never replays a rollback.

    Args:
        slug (str):
        request_id (UUID):
        scope (str | Unset):
        x_gregale_env_receipt_proof (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConfigRollbackReceipt | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            request_id=request_id,
            client=client,
            scope=scope,
            x_gregale_env_receipt_proof=x_gregale_env_receipt_proof,
        )
    ).parsed
