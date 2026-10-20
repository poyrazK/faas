from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.domain_doctor_report import DomainDoctorReport
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    domain: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/domains/{domain}/doctor".format(
            domain=quote(str(domain), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DomainDoctorReport | Problem | None:
    if response.status_code == 200:
        response_200 = DomainDoctorReport.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DomainDoctorReport | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    domain: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DomainDoctorReport | Problem]:
    """Run the 5-check domain doctor (ADR-120).

     Returns the per-domain doctor report. The five checks map
    1:1 to the Render-style custom-domain check: dns_record,
    points_to_gregale, tls_certificate, caa_permits,
    ipv6_conflict. Each check carries a Status (ok / fail /
    pending / na), Detail, Observed, Remediation, and a
    per-probe CheckedAt. Used by `gregale domains doctor
    <domain>`.

    The handler reads the latest observation row from
    `domain_doctor_observations` (the dns_poller writes a
    row every 30s). When the row is older than
    FAAS_DOMAIN_DOCTOR_TTL_SECONDS (default 300) or missing,
    the handler triggers a synchronous re-probe with a 5s
    budget. Stale=true is the visible degradation; the
    response is still 200 with the per-check Status.

    503 CodeDoctorDisabled is returned when the operator
    hasn't set FAAS_DOMAIN_DOCTOR_ENABLED. The route stays
    registered so the CLI gets a deterministic error code
    (matches the pre-#911 pattern in `api/flags.go`).

    Args:
        domain (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DomainDoctorReport | Problem]
    """

    kwargs = _get_kwargs(
        domain=domain,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    domain: str,
    *,
    client: AuthenticatedClient | Client,
) -> DomainDoctorReport | Problem | None:
    """Run the 5-check domain doctor (ADR-120).

     Returns the per-domain doctor report. The five checks map
    1:1 to the Render-style custom-domain check: dns_record,
    points_to_gregale, tls_certificate, caa_permits,
    ipv6_conflict. Each check carries a Status (ok / fail /
    pending / na), Detail, Observed, Remediation, and a
    per-probe CheckedAt. Used by `gregale domains doctor
    <domain>`.

    The handler reads the latest observation row from
    `domain_doctor_observations` (the dns_poller writes a
    row every 30s). When the row is older than
    FAAS_DOMAIN_DOCTOR_TTL_SECONDS (default 300) or missing,
    the handler triggers a synchronous re-probe with a 5s
    budget. Stale=true is the visible degradation; the
    response is still 200 with the per-check Status.

    503 CodeDoctorDisabled is returned when the operator
    hasn't set FAAS_DOMAIN_DOCTOR_ENABLED. The route stays
    registered so the CLI gets a deterministic error code
    (matches the pre-#911 pattern in `api/flags.go`).

    Args:
        domain (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DomainDoctorReport | Problem
    """

    return sync_detailed(
        domain=domain,
        client=client,
    ).parsed


async def asyncio_detailed(
    domain: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[DomainDoctorReport | Problem]:
    """Run the 5-check domain doctor (ADR-120).

     Returns the per-domain doctor report. The five checks map
    1:1 to the Render-style custom-domain check: dns_record,
    points_to_gregale, tls_certificate, caa_permits,
    ipv6_conflict. Each check carries a Status (ok / fail /
    pending / na), Detail, Observed, Remediation, and a
    per-probe CheckedAt. Used by `gregale domains doctor
    <domain>`.

    The handler reads the latest observation row from
    `domain_doctor_observations` (the dns_poller writes a
    row every 30s). When the row is older than
    FAAS_DOMAIN_DOCTOR_TTL_SECONDS (default 300) or missing,
    the handler triggers a synchronous re-probe with a 5s
    budget. Stale=true is the visible degradation; the
    response is still 200 with the per-check Status.

    503 CodeDoctorDisabled is returned when the operator
    hasn't set FAAS_DOMAIN_DOCTOR_ENABLED. The route stays
    registered so the CLI gets a deterministic error code
    (matches the pre-#911 pattern in `api/flags.go`).

    Args:
        domain (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DomainDoctorReport | Problem]
    """

    kwargs = _get_kwargs(
        domain=domain,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    domain: str,
    *,
    client: AuthenticatedClient | Client,
) -> DomainDoctorReport | Problem | None:
    """Run the 5-check domain doctor (ADR-120).

     Returns the per-domain doctor report. The five checks map
    1:1 to the Render-style custom-domain check: dns_record,
    points_to_gregale, tls_certificate, caa_permits,
    ipv6_conflict. Each check carries a Status (ok / fail /
    pending / na), Detail, Observed, Remediation, and a
    per-probe CheckedAt. Used by `gregale domains doctor
    <domain>`.

    The handler reads the latest observation row from
    `domain_doctor_observations` (the dns_poller writes a
    row every 30s). When the row is older than
    FAAS_DOMAIN_DOCTOR_TTL_SECONDS (default 300) or missing,
    the handler triggers a synchronous re-probe with a 5s
    budget. Stale=true is the visible degradation; the
    response is still 200 with the per-check Status.

    503 CodeDoctorDisabled is returned when the operator
    hasn't set FAAS_DOMAIN_DOCTOR_ENABLED. The route stays
    registered so the CLI gets a deterministic error code
    (matches the pre-#911 pattern in `api/flags.go`).

    Args:
        domain (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DomainDoctorReport | Problem
    """

    return (
        await asyncio_detailed(
            domain=domain,
            client=client,
        )
    ).parsed
