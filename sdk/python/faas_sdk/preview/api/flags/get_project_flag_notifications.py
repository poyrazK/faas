from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.flag_notification_settings import FlagNotificationSettings
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/flags/notifications".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FlagNotificationSettings | Problem | None:
    if response.status_code == 200:
        response_200 = FlagNotificationSettings.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[FlagNotificationSettings | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[FlagNotificationSettings | Problem]:
    """Read the channels an environment's flag notifications go to.

     Experiment decisions (ship a variant, keep control) and problems (guardrail rollback, sample ratio
    mismatch) of experiments without their own channels, a weekly digest of flags that need a decision
    and flag expiries go to these notification channels. Needs the flag_notifications release.

    Args:
        slug (str):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FlagNotificationSettings | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
) -> FlagNotificationSettings | Problem | None:
    """Read the channels an environment's flag notifications go to.

     Experiment decisions (ship a variant, keep control) and problems (guardrail rollback, sample ratio
    mismatch) of experiments without their own channels, a weekly digest of flags that need a decision
    and flag expiries go to these notification channels. Needs the flag_notifications release.

    Args:
        slug (str):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FlagNotificationSettings | Problem
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[FlagNotificationSettings | Problem]:
    """Read the channels an environment's flag notifications go to.

     Experiment decisions (ship a variant, keep control) and problems (guardrail rollback, sample ratio
    mismatch) of experiments without their own channels, a weekly digest of flags that need a decision
    and flag expiries go to these notification channels. Needs the flag_notifications release.

    Args:
        slug (str):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FlagNotificationSettings | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
) -> FlagNotificationSettings | Problem | None:
    """Read the channels an environment's flag notifications go to.

     Experiment decisions (ship a variant, keep control) and problems (guardrail rollback, sample ratio
    mismatch) of experiments without their own channels, a weekly digest of flags that need a decision
    and flag expiries go to these notification channels. Needs the flag_notifications release.

    Args:
        slug (str):
        environment (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FlagNotificationSettings | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
        )
    ).parsed
