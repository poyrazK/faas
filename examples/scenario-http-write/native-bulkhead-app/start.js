import { createNativeCombinedCaller } from "./combined.js";
import { createNativeBulkheadCaller } from "./server.js";
import { createNativeIsolationCaller } from "./isolation.js";
try {
  if (process.env.GREGALE_BULKHEAD_FIXTURE !== "1" && process.env.GREGALE_COMBINED_FIXTURE !== "1" && process.env.GREGALE_ISOLATION_FIXTURE !== "1") throw new Error("fixture_opt_in_required");
  const create = process.env.GREGALE_ISOLATION_FIXTURE === "1" ? createNativeIsolationCaller : process.env.GREGALE_COMBINED_FIXTURE === "1" ? createNativeCombinedCaller : createNativeBulkheadCaller;
  const fixture = create({ dependencyURL: process.env.PAYMENT_URL || "http://payment.svc.gregale:10080",
    dependencyURLs: { a: process.env.PAYMENT_URL || "http://payment.svc.gregale:10080", b: process.env.INVENTORY_URL || "http://inventory.svc.gregale:10080" },
    namespace: process.env.LEDGER_NAMESPACE, token: process.env.NATIVE_AUTH_CONTROL_TOKEN });
  fixture.server.listen(Number(process.env.PORT || 8080), process.env.HOST || "0.0.0.0");
} catch { console.error("Bulkhead fixture startup failed"); process.exitCode = 1; }
