from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.flag_experiment_ship_request import FlagExperimentShipRequest
from ...models.flag_experiment_ship_response import FlagExperimentShipResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    key: str,
    *,
    body: FlagExperimentShipRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/{slug}/environments/{environment}/experiments/{key}/ship".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
            key=quote(str(key), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FlagExperimentShipResponse | Problem | None:
    if response.status_code == 200:
        response_200 = FlagExperimentShipResponse.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[FlagExperimentShipResponse | Problem]:
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
    body: FlagExperimentShipRequest,
) -> Response[FlagExperimentShipResponse | Problem]:
    """Serve one variant to every customer of an experiment's rule.

     Publishes the flag configuration with the experiment's rule serving the variant, and stops the
    experiment if it is still running. The previous configuration version can be restored.

    Args:
        slug (str):
        environment (str):
        key (str):
        body (FlagExperimentShipRequest): The variant to ship.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FlagExperimentShipResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        key=key,
        body=body,
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
    body: FlagExperimentShipRequest,
) -> FlagExperimentShipResponse | Problem | None:
    """Serve one variant to every customer of an experiment's rule.

     Publishes the flag configuration with the experiment's rule serving the variant, and stops the
    experiment if it is still running. The previous configuration version can be restored.

    Args:
        slug (str):
        environment (str):
        key (str):
        body (FlagExperimentShipRequest): The variant to ship.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FlagExperimentShipResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        key=key,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: FlagExperimentShipRequest,
) -> Response[FlagExperimentShipResponse | Problem]:
    """Serve one variant to every customer of an experiment's rule.

     Publishes the flag configuration with the experiment's rule serving the variant, and stops the
    experiment if it is still running. The previous configuration version can be restored.

    Args:
        slug (str):
        environment (str):
        key (str):
        body (FlagExperimentShipRequest): The variant to ship.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FlagExperimentShipResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        key=key,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: FlagExperimentShipRequest,
) -> FlagExperimentShipResponse | Problem | None:
    """Serve one variant to every customer of an experiment's rule.

     Publishes the flag configuration with the experiment's rule serving the variant, and stops the
    experiment if it is still running. The previous configuration version can be restored.

    Args:
        slug (str):
        environment (str):
        key (str):
        body (FlagExperimentShipRequest): The variant to ship.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FlagExperimentShipResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            key=key,
            client=client,
            body=body,
        )
    ).parsed
