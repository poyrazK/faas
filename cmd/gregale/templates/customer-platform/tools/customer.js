import { apiClient, onboard, prepareCredential, retryCredential, id } from "./client.js";
import { draftMonth, finalizeStatement, handoffStatement } from "./billing.js";

const usage = `Usage (run locally, never in the guest):
  node tools/customer.js onboard <app-slug> <external-ref> <customer-name>
  node tools/customer.js issue <tenant-id> <consumer-id> <key-name> <private-journal>
  node tools/customer.js rotate <tenant-id> <consumer-id> <key-name> <old-key-id> <new-private-journal>
  node tools/customer.js retry <private-journal>
  node tools/customer.js usage <tenant-id> <since-ISO8601> <until-ISO8601>
  node tools/customer.js billing-month <tenant-id> <YYYY-MM>
  node tools/customer.js statement-finalize <tenant-id> <statement-id>
  node tools/customer.js statement-handoff <tenant-id> <statement-id> <external-invoice-reference>
  node tools/customer.js suspend <tenant-id>
  node tools/customer.js resume <tenant-id>`;

async function run(command, args) {
  const api = apiClient(process.env.FAAS_API, process.env.FAAS_TOKEN);
  switch (command) {
    case "onboard":
      if (args.length === 3) return onboard(api, ...args);
      break;
    case "issue":
      if (args.length === 4) return prepareCredential(api, args[0], args[1], args[2], [], args[3]);
      break;
    case "rotate":
      if (args.length === 5) return prepareCredential(api, args[0], args[1], args[2], [args[3]], args[4]);
      break;
    case "retry":
      if (args.length === 1) return retryCredential(api, args[0]);
      break;
    case "billing-month":
      if (args.length === 2) return draftMonth(api, ...args);
      break;
    case "statement-finalize":
      if (args.length === 2) return finalizeStatement(api, ...args);
      break;
    case "statement-handoff":
      if (args.length === 3) return handoffStatement(api, ...args);
      break;
    case "usage":
      if (args.length === 3) {
        id(args[0]);
        const [since, until] = args.slice(1).map((value) => new Date(value));
        if (!Number.isFinite(+since) || !Number.isFinite(+until) || since >= until) {
          throw new Error("Supply a valid increasing usage time range");
        }
        const query = new URLSearchParams({ since: since.toISOString(), until: until.toISOString() });
        return api.request("GET", `/v1/account/platform-tenants/${args[0]}/usage?${query}`);
      }
      break;
    case "suspend": case "resume":
      if (args.length === 1) return api.request("PATCH", `/v1/account/platform-tenants/${id(args[0])}`,
        { status: command === "suspend" ? "suspended" : "active" });
      break;
  }
  throw new Error(usage);
}

const [command, ...args] = process.argv.slice(2);
if (!command || command === "--help") console.log(usage);
else {
  try { console.log(JSON.stringify(await run(command, args), null, 2)); }
  catch (error) {
    // Built-in filesystem/URL/JSON exceptions may include paths or input.
    console.error(error.constructor === Error ? error.message : "Invalid arguments or private journal file");
    process.exitCode = 1;
  }
}
