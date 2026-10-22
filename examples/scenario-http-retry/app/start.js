import { createRetryServer } from "./server.js";
createRetryServer({ dependencyURL: process.env.DEPENDENCY_URL || "http://inventory.svc.gregale:10080/" })
  .listen(Number(process.env.PORT || 8080), process.env.HOST || "0.0.0.0");
