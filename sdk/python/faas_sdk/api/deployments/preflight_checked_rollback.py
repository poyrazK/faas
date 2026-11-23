from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.binding_check_finding import BindingCheckFinding
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    expected_current_deployment_id: str,
    target_deployment_id: str,
    acknowledge_releases: bool | Unset = False,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["expected_current_deployment_id"] = expected_current_deployment_id

    params["target_deployment_id"] = target_deployment_id

    params["acknowledge_releases"] = acknowledge_releases

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/rollbacks/preflight".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | list[BindingCheckFinding]:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = BindingCheckFinding.from_dict(response_200_item_data)

            response_200.append(response_200_item)

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

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | list[BindingCheckFinding]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    expected_current_deployment_id: str,
    target_deployment_id: str,
    acknowledge_releases: bool | Unset = False,
) -> Response[Problem | list[BindingCheckFinding]]:
    """Check an exact rollback pair before admission.

     Reports known blockers for the current and target revisions. A successful check returns warnings,
    including any binding verification that requires a prepared target. Admission and routing recheck
    current conditions.

    Args:
        slug (str):
        expected_current_deployment_id (str):
        target_deployment_id (str):
        acknowledge_releases (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[BindingCheckFinding]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        expected_current_deployment_id=expected_current_deployment_id,
        target_deployment_id=target_deployment_id,
        acknowledge_releases=acknowledge_releases,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    expected_current_deployment_id: str,
    target_deployment_id: str,
    acknowledge_releases: bool | Unset = False,
) -> Problem | list[BindingCheckFinding] | None:
    """Check an exact rollback pair before admission.

     Reports known blockers for the current and target revisions. A successful check returns warnings,
    including any binding verification that requires a prepared target. Admission and routing recheck
    current conditions.

    Args:
        slug (str):
        expected_current_deployment_id (str):
        target_deployment_id (str):
        acknowledge_releases (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[BindingCheckFinding]
    """

    return sync_detailed(
        slug=slug,
        client=client,
        expected_current_deployment_id=expected_current_deployment_id,
        target_deployment_id=target_deployment_id,
        acknowledge_releases=acknowledge_releases,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    expected_current_deployment_id: str,
    target_deployment_id: str,
    acknowledge_releases: bool | Unset = False,
) -> Response[Problem | list[BindingCheckFinding]]:
    """Check an exact rollback pair before admission.

     Reports known blockers for the current and target revisions. A successful check returns warnings,
    including any binding verification that requires a prepared target. Admission and routing recheck
    current conditions.

    Args:
        slug (str):
        expected_current_deployment_id (str):
        target_deployment_id (str):
        acknowledge_releases (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[BindingCheckFinding]]
    """

    kwargs = _get_kwargs(
        slug=slug,
        expected_current_deployment_id=expected_current_deployment_id,
        target_deployment_id=target_deployment_id,
        acknowledge_releases=acknowledge_releases,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    expected_current_deployment_id: str,
    target_deployment_id: str,
    acknowledge_releases: bool | Unset = False,
) -> Problem | list[BindingCheckFinding] | None:
    """Check an exact rollback pair before admission.

     Reports known blockers for the current and target revisions. A successful check returns warnings,
    including any binding verification that requires a prepared target. Admission and routing recheck
    current conditions.

    Args:
        slug (str):
        expected_current_deployment_id (str):
        target_deployment_id (str):
        acknowledge_releases (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[BindingCheckFinding]
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            expected_current_deployment_id=expected_current_deployment_id,
            target_deployment_id=target_deployment_id,
            acknowledge_releases=acknowledge_releases,
        )
    ).parsed
