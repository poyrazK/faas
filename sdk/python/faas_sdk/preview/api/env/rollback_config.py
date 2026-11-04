from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.config_rollback_request import ConfigRollbackRequest
from ...models.config_rollback_response import ConfigRollbackResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: ConfigRollbackRequest,
    scope: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["scope"] = scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/config/rollback".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ConfigRollbackResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ConfigRollbackResponse.from_dict(response.json())

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
) -> Response[ConfigRollbackResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ConfigRollbackRequest,
    scope: str | Unset = UNSET,
) -> Response[ConfigRollbackResponse | Problem]:
    """Restore a scope's variables to a config revision.

     Sets and deletes variables until the scope matches the revision, as one guarded change. Secrets
    cannot be restored from a revision; those that differ are listed in `secrets_not_restored`.

    Args:
        slug (str):
        scope (str | Unset):
        body (ConfigRollbackRequest): The config revision whose variables a rollback restores.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConfigRollbackResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        scope=scope,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ConfigRollbackRequest,
    scope: str | Unset = UNSET,
) -> ConfigRollbackResponse | Problem | None:
    """Restore a scope's variables to a config revision.

     Sets and deletes variables until the scope matches the revision, as one guarded change. Secrets
    cannot be restored from a revision; those that differ are listed in `secrets_not_restored`.

    Args:
        slug (str):
        scope (str | Unset):
        body (ConfigRollbackRequest): The config revision whose variables a rollback restores.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConfigRollbackResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        scope=scope,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ConfigRollbackRequest,
    scope: str | Unset = UNSET,
) -> Response[ConfigRollbackResponse | Problem]:
    """Restore a scope's variables to a config revision.

     Sets and deletes variables until the scope matches the revision, as one guarded change. Secrets
    cannot be restored from a revision; those that differ are listed in `secrets_not_restored`.

    Args:
        slug (str):
        scope (str | Unset):
        body (ConfigRollbackRequest): The config revision whose variables a rollback restores.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConfigRollbackResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        scope=scope,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ConfigRollbackRequest,
    scope: str | Unset = UNSET,
) -> ConfigRollbackResponse | Problem | None:
    """Restore a scope's variables to a config revision.

     Sets and deletes variables until the scope matches the revision, as one guarded change. Secrets
    cannot be restored from a revision; those that differ are listed in `secrets_not_restored`.

    Args:
        slug (str):
        scope (str | Unset):
        body (ConfigRollbackRequest): The config revision whose variables a rollback restores.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConfigRollbackResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            scope=scope,
        )
    ).parsed
