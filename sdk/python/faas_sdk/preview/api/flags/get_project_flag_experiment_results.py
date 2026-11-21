from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.flag_experiment_results import FlagExperimentResults
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    environment: str,
    key: str,
    *,
    segment: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["segment"] = segment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/experiments/{key}/results".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
            key=quote(str(key), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FlagExperimentResults | Problem | None:
    if response.status_code == 200:
        response_200 = FlagExperimentResults.from_dict(response.json())

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[FlagExperimentResults | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    segment: str | Unset = UNSET,
) -> Response[FlagExperimentResults | Problem]:
    """Read a flag experiment's sequential results.

     Each variant is compared with control by an always-valid mixture sequential probability ratio test,
    so results may be read at any time. A sample ratio mismatch against the rule's weights is reported
    first. Ratio metrics use the delta method; percentile metrics a fixed-horizon bootstrap; bandits
    report each arm's best-arm probability and expected loss instead of p-values.

    Args:
        slug (str):
        environment (str):
        key (str):
        segment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FlagExperimentResults | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        key=key,
        segment=segment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    segment: str | Unset = UNSET,
) -> FlagExperimentResults | Problem | None:
    """Read a flag experiment's sequential results.

     Each variant is compared with control by an always-valid mixture sequential probability ratio test,
    so results may be read at any time. A sample ratio mismatch against the rule's weights is reported
    first. Ratio metrics use the delta method; percentile metrics a fixed-horizon bootstrap; bandits
    report each arm's best-arm probability and expected loss instead of p-values.

    Args:
        slug (str):
        environment (str):
        key (str):
        segment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FlagExperimentResults | Problem
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        key=key,
        client=client,
        segment=segment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    segment: str | Unset = UNSET,
) -> Response[FlagExperimentResults | Problem]:
    """Read a flag experiment's sequential results.

     Each variant is compared with control by an always-valid mixture sequential probability ratio test,
    so results may be read at any time. A sample ratio mismatch against the rule's weights is reported
    first. Ratio metrics use the delta method; percentile metrics a fixed-horizon bootstrap; bandits
    report each arm's best-arm probability and expected loss instead of p-values.

    Args:
        slug (str):
        environment (str):
        key (str):
        segment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FlagExperimentResults | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        key=key,
        segment=segment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    segment: str | Unset = UNSET,
) -> FlagExperimentResults | Problem | None:
    """Read a flag experiment's sequential results.

     Each variant is compared with control by an always-valid mixture sequential probability ratio test,
    so results may be read at any time. A sample ratio mismatch against the rule's weights is reported
    first. Ratio metrics use the delta method; percentile metrics a fixed-horizon bootstrap; bandits
    report each arm's best-arm probability and expected loss instead of p-values.

    Args:
        slug (str):
        environment (str):
        key (str):
        segment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FlagExperimentResults | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            key=key,
            client=client,
            segment=segment,
        )
    ).parsed
