from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.flag_experiment_holdout import FlagExperimentHoldout
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    body: FlagExperimentHoldout,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/projects/{slug}/environments/{environment}/experiment-holdout".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FlagExperimentHoldout | Problem | None:
    if response.status_code == 200:
        response_200 = FlagExperimentHoldout.from_dict(response.json())

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
) -> Response[FlagExperimentHoldout | Problem]:
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
    body: FlagExperimentHoldout,
) -> Response[FlagExperimentHoldout | Problem]:
    """Set the environment holdout.

     0 to 2000 basis points; 0 turns it off. Refused with 409 flag_experiment_running while an experiment
    runs, so no experiment's population changes under it.

    Args:
        slug (str):
        environment (str):
        body (FlagExperimentHoldout): The environment holdout: basis points of customers that see
            every experiment's control.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FlagExperimentHoldout | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
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
    body: FlagExperimentHoldout,
) -> FlagExperimentHoldout | Problem | None:
    """Set the environment holdout.

     0 to 2000 basis points; 0 turns it off. Refused with 409 flag_experiment_running while an experiment
    runs, so no experiment's population changes under it.

    Args:
        slug (str):
        environment (str):
        body (FlagExperimentHoldout): The environment holdout: basis points of customers that see
            every experiment's control.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FlagExperimentHoldout | Problem
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: FlagExperimentHoldout,
) -> Response[FlagExperimentHoldout | Problem]:
    """Set the environment holdout.

     0 to 2000 basis points; 0 turns it off. Refused with 409 flag_experiment_running while an experiment
    runs, so no experiment's population changes under it.

    Args:
        slug (str):
        environment (str):
        body (FlagExperimentHoldout): The environment holdout: basis points of customers that see
            every experiment's control.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FlagExperimentHoldout | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: FlagExperimentHoldout,
) -> FlagExperimentHoldout | Problem | None:
    """Set the environment holdout.

     0 to 2000 basis points; 0 turns it off. Refused with 409 flag_experiment_running while an experiment
    runs, so no experiment's population changes under it.

    Args:
        slug (str):
        environment (str):
        body (FlagExperimentHoldout): The environment holdout: basis points of customers that see
            every experiment's control.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FlagExperimentHoldout | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            body=body,
        )
    ).parsed
