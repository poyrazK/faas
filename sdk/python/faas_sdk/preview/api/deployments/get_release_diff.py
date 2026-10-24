from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.release_diff import ReleaseDiff
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    against: UUID | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_against: str | Unset = UNSET
    if not isinstance(against, Unset):
        json_against = str(against)
    params["against"] = json_against

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/deployments/{id}/diff".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Problem | ReleaseDiff | None:
    if response.status_code == 200:
        response_200 = ReleaseDiff.from_dict(response.json())

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
) -> Response[Problem | ReleaseDiff]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    against: UUID | Unset = UNSET,
) -> Response[Problem | ReleaseDiff]:
    """Show what changed between two releases (ADR-20261021101117).

     Compares the code, configuration keys, packages, runtime settings and release command of two
    releases of one app. Never returns variable values, secret fingerprints or SBOM files. A section
    whose history is missing reports known=false with a note.

    Args:
        id (str):
        against (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ReleaseDiff]
    """

    kwargs = _get_kwargs(
        id=id,
        against=against,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    against: UUID | Unset = UNSET,
) -> Problem | ReleaseDiff | None:
    """Show what changed between two releases (ADR-20261021101117).

     Compares the code, configuration keys, packages, runtime settings and release command of two
    releases of one app. Never returns variable values, secret fingerprints or SBOM files. A section
    whose history is missing reports known=false with a note.

    Args:
        id (str):
        against (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ReleaseDiff
    """

    return sync_detailed(
        id=id,
        client=client,
        against=against,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    against: UUID | Unset = UNSET,
) -> Response[Problem | ReleaseDiff]:
    """Show what changed between two releases (ADR-20261021101117).

     Compares the code, configuration keys, packages, runtime settings and release command of two
    releases of one app. Never returns variable values, secret fingerprints or SBOM files. A section
    whose history is missing reports known=false with a note.

    Args:
        id (str):
        against (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ReleaseDiff]
    """

    kwargs = _get_kwargs(
        id=id,
        against=against,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    against: UUID | Unset = UNSET,
) -> Problem | ReleaseDiff | None:
    """Show what changed between two releases (ADR-20261021101117).

     Compares the code, configuration keys, packages, runtime settings and release command of two
    releases of one app. Never returns variable values, secret fingerprints or SBOM files. A section
    whose history is missing reports known=false with a note.

    Args:
        id (str):
        against (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ReleaseDiff
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            against=against,
        )
    ).parsed
