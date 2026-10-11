from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_doctor_response import OperationDoctorResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    deployment_id: UUID,
    *,
    tenant_id: UUID,
    name: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_tenant_id = str(tenant_id)
    params["tenant_id"] = json_tenant_id

    params["name"] = name

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/deployments/{deployment_id}/operation-doctor".format(
            slug=quote(str(slug), safe=""),
            deployment_id=quote(str(deployment_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationDoctorResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationDoctorResponse.from_dict(response.json())

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
) -> Response[OperationDoctorResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    tenant_id: UUID,
    name: str | Unset = UNSET,
) -> Response[OperationDoctorResponse | Problem]:
    """Observe Operations submission prerequisites on the responding API node.

     Account read scope and MFA are required. Select an owned deployment and tenant, optionally one
    operation name. This read reserves no quota, grants no admission, probes no external services and
    does not qualify native lifecycle or fleet availability. Completion warnings are independent of
    submission blockers. Eligibility is an advisory observation, not an execution guarantee.

    Args:
        slug (str):
        deployment_id (UUID):
        tenant_id (UUID):
        name (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationDoctorResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_id=deployment_id,
        tenant_id=tenant_id,
        name=name,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    tenant_id: UUID,
    name: str | Unset = UNSET,
) -> OperationDoctorResponse | Problem | None:
    """Observe Operations submission prerequisites on the responding API node.

     Account read scope and MFA are required. Select an owned deployment and tenant, optionally one
    operation name. This read reserves no quota, grants no admission, probes no external services and
    does not qualify native lifecycle or fleet availability. Completion warnings are independent of
    submission blockers. Eligibility is an advisory observation, not an execution guarantee.

    Args:
        slug (str):
        deployment_id (UUID):
        tenant_id (UUID):
        name (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationDoctorResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        deployment_id=deployment_id,
        client=client,
        tenant_id=tenant_id,
        name=name,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    tenant_id: UUID,
    name: str | Unset = UNSET,
) -> Response[OperationDoctorResponse | Problem]:
    """Observe Operations submission prerequisites on the responding API node.

     Account read scope and MFA are required. Select an owned deployment and tenant, optionally one
    operation name. This read reserves no quota, grants no admission, probes no external services and
    does not qualify native lifecycle or fleet availability. Completion warnings are independent of
    submission blockers. Eligibility is an advisory observation, not an execution guarantee.

    Args:
        slug (str):
        deployment_id (UUID):
        tenant_id (UUID):
        name (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationDoctorResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_id=deployment_id,
        tenant_id=tenant_id,
        name=name,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    tenant_id: UUID,
    name: str | Unset = UNSET,
) -> OperationDoctorResponse | Problem | None:
    """Observe Operations submission prerequisites on the responding API node.

     Account read scope and MFA are required. Select an owned deployment and tenant, optionally one
    operation name. This read reserves no quota, grants no admission, probes no external services and
    does not qualify native lifecycle or fleet availability. Completion warnings are independent of
    submission blockers. Eligibility is an advisory observation, not an execution guarantee.

    Args:
        slug (str):
        deployment_id (UUID):
        tenant_id (UUID):
        name (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationDoctorResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment_id=deployment_id,
            client=client,
            tenant_id=tenant_id,
            name=name,
        )
    ).parsed
