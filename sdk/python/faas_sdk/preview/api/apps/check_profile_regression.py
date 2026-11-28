from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.check_profile_regression_request import CheckProfileRegressionRequest
from ...models.problem import Problem
from ...models.profile_investigation_response import ProfileInvestigationResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    body: CheckProfileRegressionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/profiles/investigations/{id}/check".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProfileInvestigationResponse | None:
    if response.status_code == 200:
        response_200 = ProfileInvestigationResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[Problem | ProfileInvestigationResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CheckProfileRegressionRequest,
) -> Response[Problem | ProfileInvestigationResponse]:
    """Check a saved comparison for CPU regressions.

     Checks the exact saved windows with deploy-write access. Both relative and absolute CPU-rate
    thresholds must be met for a total, comparable function or complete call path. Missing samples,
    insufficient capture coverage, recorded failures or unavailable history produce a stored
    inconclusive assessment. This heuristic does not establish deployment causality or statistical
    confidence. Returns a new revision; concurrent edits or checks return 409. Later edits make the
    preserved assessment stale when its investigation_revision differs from the saved revision. Existing
    profiling query limits and retention apply; raw profiles are never stored.

    Args:
        slug (str):
        id (UUID):
        body (CheckProfileRegressionRequest): Revision-protected on-demand assessment request.
            Omitted options use CPU/s, 20 percent, 0.01 CPU/s, three profiles and 0.8 capture ratio.
            CPU/request mode requires both request-specific options.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProfileInvestigationResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CheckProfileRegressionRequest,
) -> Problem | ProfileInvestigationResponse | None:
    """Check a saved comparison for CPU regressions.

     Checks the exact saved windows with deploy-write access. Both relative and absolute CPU-rate
    thresholds must be met for a total, comparable function or complete call path. Missing samples,
    insufficient capture coverage, recorded failures or unavailable history produce a stored
    inconclusive assessment. This heuristic does not establish deployment causality or statistical
    confidence. Returns a new revision; concurrent edits or checks return 409. Later edits make the
    preserved assessment stale when its investigation_revision differs from the saved revision. Existing
    profiling query limits and retention apply; raw profiles are never stored.

    Args:
        slug (str):
        id (UUID):
        body (CheckProfileRegressionRequest): Revision-protected on-demand assessment request.
            Omitted options use CPU/s, 20 percent, 0.01 CPU/s, three profiles and 0.8 capture ratio.
            CPU/request mode requires both request-specific options.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProfileInvestigationResponse
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CheckProfileRegressionRequest,
) -> Response[Problem | ProfileInvestigationResponse]:
    """Check a saved comparison for CPU regressions.

     Checks the exact saved windows with deploy-write access. Both relative and absolute CPU-rate
    thresholds must be met for a total, comparable function or complete call path. Missing samples,
    insufficient capture coverage, recorded failures or unavailable history produce a stored
    inconclusive assessment. This heuristic does not establish deployment causality or statistical
    confidence. Returns a new revision; concurrent edits or checks return 409. Later edits make the
    preserved assessment stale when its investigation_revision differs from the saved revision. Existing
    profiling query limits and retention apply; raw profiles are never stored.

    Args:
        slug (str):
        id (UUID):
        body (CheckProfileRegressionRequest): Revision-protected on-demand assessment request.
            Omitted options use CPU/s, 20 percent, 0.01 CPU/s, three profiles and 0.8 capture ratio.
            CPU/request mode requires both request-specific options.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProfileInvestigationResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CheckProfileRegressionRequest,
) -> Problem | ProfileInvestigationResponse | None:
    """Check a saved comparison for CPU regressions.

     Checks the exact saved windows with deploy-write access. Both relative and absolute CPU-rate
    thresholds must be met for a total, comparable function or complete call path. Missing samples,
    insufficient capture coverage, recorded failures or unavailable history produce a stored
    inconclusive assessment. This heuristic does not establish deployment causality or statistical
    confidence. Returns a new revision; concurrent edits or checks return 409. Later edits make the
    preserved assessment stale when its investigation_revision differs from the saved revision. Existing
    profiling query limits and retention apply; raw profiles are never stored.

    Args:
        slug (str):
        id (UUID):
        body (CheckProfileRegressionRequest): Revision-protected on-demand assessment request.
            Omitted options use CPU/s, 20 percent, 0.01 CPU/s, three profiles and 0.8 capture ratio.
            CPU/request mode requires both request-specific options.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProfileInvestigationResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
