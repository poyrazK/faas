import { readFile } from "node:fs/promises";

export async function csvApplicationToolkit({
  sdk,
  adapters,
  recovery,
  refund,
  workInbox,
  extraMounts = [],
}) {
  const rules = JSON.parse(
    await readFile(
      new URL("./application-toolkit.json", import.meta.url),
      "utf8",
    ),
  );
  const [report] = adapters.failure.resourceTemplates;
  const mounts = [
    ...Object.entries(adapters).map(([kind, adapter]) => ({
      adapter,
      toolScopes: rules[kind],
      ...(kind === "failure"
        ? { resourceScopes: { [report.uriTemplate]: ["reports:read"] } }
        : {}),
    })),
    ...(workInbox
      ? [
          {
            adapter: sdk.createQueryToolMCP(workInbox),
            toolScopes: rules.inbox,
          },
        ]
      : []),
    { adapter: sdk.createCompensationMCP(recovery), toolScopes: rules.cleanup },
    { adapter: sdk.createOutboundActionMCP(refund), toolScopes: rules.refund },
    ...extraMounts,
  ];
  return workInbox
    ? sdk.createApplicationToolkit(mounts, {
        workInboxFollowUp: { inbox: workInbox, scopes: rules.inbox.work_inbox },
      })
    : sdk.createApplicationToolkit(mounts);
}
