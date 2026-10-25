from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.project_binding_verification_run import ProjectBindingVerificationRun
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    run: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/release-sets/verification-runs/{run}".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
            run=quote(str(run), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ProjectBindingVerificationRun | None:
    if response.status_code == 200:
        response_200 = ProjectBindingVerificationRun.from_dict(response.json())

        return response_200

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 429:
        response_429 = cast(Any, None)
        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ProjectBindingVerificationRun]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    run: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ProjectBindingVerificationRun]:
    """getProjectBindingVerificationRun

    Args:
        slug (str):
        environment (str):
        run (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ProjectBindingVerificationRun]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        run=run,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    run: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | ProjectBindingVerificationRun | None:
    """getProjectBindingVerificationRun

    Args:
        slug (str):
        environment (str):
        run (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ProjectBindingVerificationRun
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        run=run,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    run: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ProjectBindingVerificationRun]:
    """getProjectBindingVerificationRun

    Args:
        slug (str):
        environment (str):
        run (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ProjectBindingVerificationRun]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        run=run,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    run: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | ProjectBindingVerificationRun | None:
    """getProjectBindingVerificationRun

    Args:
        slug (str):
        environment (str):
        run (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ProjectBindingVerificationRun
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            run=run,
            client=client,
        )
    ).parsed
