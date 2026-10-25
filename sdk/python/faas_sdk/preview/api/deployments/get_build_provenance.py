from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.build_provenance_response import BuildProvenanceResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/builds/{id}/provenance".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> BuildProvenanceResponse | Problem | None:
    if response.status_code == 200:
        response_200 = BuildProvenanceResponse.from_dict(response.json())

        return response_200

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
) -> Response[BuildProvenanceResponse | Problem]:
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
) -> Response[BuildProvenanceResponse | Problem]:
    r"""Get build provenance.

     Returns the ADR-038 `build_provenance` row for a single build.
    Each successful build produces exactly one provenance row
    (builderd's populator runs at the `markSucceeded` sites); the
    row is the customer-visible \"what ran?\" record: buildkit /
    railpack version, base / runner digests, source URL + commit
    SHA, plan, builder node ID, and the build's started_at /
    finished_at timestamps.

    A 404 with `code=build_provenance_not_found` is returned when
    the build exists but no provenance row landed (the populator
    logs a WARN inside builderd on a failed INSERT — the build
    itself still succeeded). A 404 with `code=not_found` is
    returned when no build row matches the id, or when the
    build's owning app belongs to a different account.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BuildProvenanceResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> BuildProvenanceResponse | Problem | None:
    r"""Get build provenance.

     Returns the ADR-038 `build_provenance` row for a single build.
    Each successful build produces exactly one provenance row
    (builderd's populator runs at the `markSucceeded` sites); the
    row is the customer-visible \"what ran?\" record: buildkit /
    railpack version, base / runner digests, source URL + commit
    SHA, plan, builder node ID, and the build's started_at /
    finished_at timestamps.

    A 404 with `code=build_provenance_not_found` is returned when
    the build exists but no provenance row landed (the populator
    logs a WARN inside builderd on a failed INSERT — the build
    itself still succeeded). A 404 with `code=not_found` is
    returned when no build row matches the id, or when the
    build's owning app belongs to a different account.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BuildProvenanceResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[BuildProvenanceResponse | Problem]:
    r"""Get build provenance.

     Returns the ADR-038 `build_provenance` row for a single build.
    Each successful build produces exactly one provenance row
    (builderd's populator runs at the `markSucceeded` sites); the
    row is the customer-visible \"what ran?\" record: buildkit /
    railpack version, base / runner digests, source URL + commit
    SHA, plan, builder node ID, and the build's started_at /
    finished_at timestamps.

    A 404 with `code=build_provenance_not_found` is returned when
    the build exists but no provenance row landed (the populator
    logs a WARN inside builderd on a failed INSERT — the build
    itself still succeeded). A 404 with `code=not_found` is
    returned when no build row matches the id, or when the
    build's owning app belongs to a different account.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BuildProvenanceResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> BuildProvenanceResponse | Problem | None:
    r"""Get build provenance.

     Returns the ADR-038 `build_provenance` row for a single build.
    Each successful build produces exactly one provenance row
    (builderd's populator runs at the `markSucceeded` sites); the
    row is the customer-visible \"what ran?\" record: buildkit /
    railpack version, base / runner digests, source URL + commit
    SHA, plan, builder node ID, and the build's started_at /
    finished_at timestamps.

    A 404 with `code=build_provenance_not_found` is returned when
    the build exists but no provenance row landed (the populator
    logs a WARN inside builderd on a failed INSERT — the build
    itself still succeeded). A 404 with `code=not_found` is
    returned when no build row matches the id, or when the
    build's owning app belongs to a different account.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BuildProvenanceResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
