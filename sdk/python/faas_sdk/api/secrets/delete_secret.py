from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_secret_revocation_response import AppSecretRevocationResponse
from ...models.delete_secret_prefer import DeleteSecretPrefer
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    key: str,
    *,
    scope: str | Unset = UNSET,
    prefer: DeleteSecretPrefer | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(prefer, Unset):
        headers["Prefer"] = str(prefer)

    params: dict[str, Any] = {}

    params["scope"] = scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/apps/{slug}/secrets/{key}".format(
            slug=quote(str(slug), safe=""),
            key=quote(str(key), safe=""),
        ),
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | AppSecretRevocationResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppSecretRevocationResponse.from_dict(response.json())

        return response_200

    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

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
) -> Response[Any | AppSecretRevocationResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
    prefer: DeleteSecretPrefer | Unset = UNSET,
) -> Response[Any | AppSecretRevocationResponse | Problem]:
    """Delete a sealed secret.

     Deletes the sealed value. The legacy default is 204 No Content; send `Prefer: return=representation`
    to receive the durable, value-free acknowledgement record for the active authorized runtime roster.

    Args:
        slug (str):
        key (str):
        scope (str | Unset):
        prefer (DeleteSecretPrefer | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | AppSecretRevocationResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        key=key,
        scope=scope,
        prefer=prefer,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
    prefer: DeleteSecretPrefer | Unset = UNSET,
) -> Any | AppSecretRevocationResponse | Problem | None:
    """Delete a sealed secret.

     Deletes the sealed value. The legacy default is 204 No Content; send `Prefer: return=representation`
    to receive the durable, value-free acknowledgement record for the active authorized runtime roster.

    Args:
        slug (str):
        key (str):
        scope (str | Unset):
        prefer (DeleteSecretPrefer | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | AppSecretRevocationResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        key=key,
        client=client,
        scope=scope,
        prefer=prefer,
    ).parsed


async def asyncio_detailed(
    slug: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
    prefer: DeleteSecretPrefer | Unset = UNSET,
) -> Response[Any | AppSecretRevocationResponse | Problem]:
    """Delete a sealed secret.

     Deletes the sealed value. The legacy default is 204 No Content; send `Prefer: return=representation`
    to receive the durable, value-free acknowledgement record for the active authorized runtime roster.

    Args:
        slug (str):
        key (str):
        scope (str | Unset):
        prefer (DeleteSecretPrefer | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | AppSecretRevocationResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        key=key,
        scope=scope,
        prefer=prefer,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
    prefer: DeleteSecretPrefer | Unset = UNSET,
) -> Any | AppSecretRevocationResponse | Problem | None:
    """Delete a sealed secret.

     Deletes the sealed value. The legacy default is 204 No Content; send `Prefer: return=representation`
    to receive the durable, value-free acknowledgement record for the active authorized runtime roster.

    Args:
        slug (str):
        key (str):
        scope (str | Unset):
        prefer (DeleteSecretPrefer | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | AppSecretRevocationResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            key=key,
            client=client,
            scope=scope,
            prefer=prefer,
        )
    ).parsed
