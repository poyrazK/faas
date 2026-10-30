from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.release_timeline import ReleaseTimeline
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/deployments/{id}/timeline".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ReleaseTimeline | None:
    if response.status_code == 200:
        response_200 = ReleaseTimeline.from_dict(response.json())

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
) -> Response[Problem | ReleaseTimeline]:
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
) -> Response[Problem | ReleaseTimeline]:
    """Show a release's phase and history (ADR-20261022120744).

     Composes what Gregale recorded about one release into a phase, a one-line summary and its events,
    oldest first: creation, schedule, approvals, rollout start, each canary step with the decision and
    gate results behind it, holds, completion, the post-release watch, abort or rollback, and the
    release that replaced it. Reads at most 500 audit rows; truncated=true means older history was left
    out.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ReleaseTimeline]
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
) -> Problem | ReleaseTimeline | None:
    """Show a release's phase and history (ADR-20261022120744).

     Composes what Gregale recorded about one release into a phase, a one-line summary and its events,
    oldest first: creation, schedule, approvals, rollout start, each canary step with the decision and
    gate results behind it, holds, completion, the post-release watch, abort or rollback, and the
    release that replaced it. Reads at most 500 audit rows; truncated=true means older history was left
    out.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ReleaseTimeline
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ReleaseTimeline]:
    """Show a release's phase and history (ADR-20261022120744).

     Composes what Gregale recorded about one release into a phase, a one-line summary and its events,
    oldest first: creation, schedule, approvals, rollout start, each canary step with the decision and
    gate results behind it, holds, completion, the post-release watch, abort or rollback, and the
    release that replaced it. Reads at most 500 audit rows; truncated=true means older history was left
    out.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ReleaseTimeline]
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
) -> Problem | ReleaseTimeline | None:
    """Show a release's phase and history (ADR-20261022120744).

     Composes what Gregale recorded about one release into a phase, a one-line summary and its events,
    oldest first: creation, schedule, approvals, rollout start, each canary step with the decision and
    gate results behind it, holds, completion, the post-release watch, abort or rollback, and the
    release that replaced it. Reads at most 500 audit rows; truncated=true means older history was left
    out.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ReleaseTimeline
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
