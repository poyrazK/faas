from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.issue_release_comparison import IssueReleaseComparison
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    baseline: UUID,
    candidate: UUID,
    environment: str | Unset = "application",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_baseline = str(baseline)
    params["baseline"] = json_baseline

    json_candidate = str(candidate)
    params["candidate"] = json_candidate

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/issue-release-comparison".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> IssueReleaseComparison | Problem | None:
    if response.status_code == 200:
        response_200 = IssueReleaseComparison.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[IssueReleaseComparison | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    baseline: UUID,
    candidate: UUID,
    environment: str | Unset = "application",
) -> Response[IssueReleaseComparison | Problem]:
    """Compare observed release request health and issue signals over the last 24 hours.

     Request counts expand telemetry row weights and span all traffic for each deployment. Issue signals
    use the selected environment. These are independent retained observations, not complete traffic,
    crash-free sessions, or a controlled experiment. Unknown request rates and deltas are null.

    Args:
        slug (str):
        baseline (UUID):
        candidate (UUID):
        environment (str | Unset):  Default: 'application'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IssueReleaseComparison | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        baseline=baseline,
        candidate=candidate,
        environment=environment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    baseline: UUID,
    candidate: UUID,
    environment: str | Unset = "application",
) -> IssueReleaseComparison | Problem | None:
    """Compare observed release request health and issue signals over the last 24 hours.

     Request counts expand telemetry row weights and span all traffic for each deployment. Issue signals
    use the selected environment. These are independent retained observations, not complete traffic,
    crash-free sessions, or a controlled experiment. Unknown request rates and deltas are null.

    Args:
        slug (str):
        baseline (UUID):
        candidate (UUID):
        environment (str | Unset):  Default: 'application'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IssueReleaseComparison | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        baseline=baseline,
        candidate=candidate,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    baseline: UUID,
    candidate: UUID,
    environment: str | Unset = "application",
) -> Response[IssueReleaseComparison | Problem]:
    """Compare observed release request health and issue signals over the last 24 hours.

     Request counts expand telemetry row weights and span all traffic for each deployment. Issue signals
    use the selected environment. These are independent retained observations, not complete traffic,
    crash-free sessions, or a controlled experiment. Unknown request rates and deltas are null.

    Args:
        slug (str):
        baseline (UUID):
        candidate (UUID):
        environment (str | Unset):  Default: 'application'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IssueReleaseComparison | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        baseline=baseline,
        candidate=candidate,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    baseline: UUID,
    candidate: UUID,
    environment: str | Unset = "application",
) -> IssueReleaseComparison | Problem | None:
    """Compare observed release request health and issue signals over the last 24 hours.

     Request counts expand telemetry row weights and span all traffic for each deployment. Issue signals
    use the selected environment. These are independent retained observations, not complete traffic,
    crash-free sessions, or a controlled experiment. Unknown request rates and deltas are null.

    Args:
        slug (str):
        baseline (UUID):
        candidate (UUID):
        environment (str | Unset):  Default: 'application'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IssueReleaseComparison | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            baseline=baseline,
            candidate=candidate,
            environment=environment,
        )
    ).parsed
