import { createBoundedConnectionPool } from "./pool-server.js";
import { createEchoDependency, createRecoveryProbe } from "./server.js";

const campaign = process.env.GREGALE_TCP_CAMPAIGN_FIXTURE === "1";
createEchoDependency({ maxBytes: campaign ? 16384 : 32 }).listen(6379, "0.0.0.0");
const options = {
  cancellationFixture: process.env.GREGALE_POOL_CANCEL_FIXTURE === "1",
  namespace: process.env.GREGALE_POOL_NAMESPACE,
  overallDeadlineMS: process.env.GREGALE_DEADLINE_FIXTURE === "1" ? 500 : 0,
  payloadBytes: campaign ? 16384 : 6,
  dependencyHost: process.env.PROBE_DEPENDENCY_HOST,
  dependencyPort: Number(process.env.PROBE_DEPENDENCY_PORT || 6379),
  delayMS: Number(process.env.PROBE_DELAY_MS || 25),
};
const server = process.env.GREGALE_POOL_FIXTURE === "1" ? createBoundedConnectionPool(options).server : createRecoveryProbe(options);
server.listen(Number(process.env.PORT || 8080), process.env.HOST || "0.0.0.0");
