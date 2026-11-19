const { createServer } = require("node:http");

createServer((request, response) => {
  if (request.method === "GET" && request.url === "/") {
    response.setHeader("Content-Type", "application/json");
    response.end(JSON.stringify({ message: "Hello from private Node" }));
    return;
  }

  if (request.method === "GET" && request.url === "/healthz") {
    response.end("ok");
    return;
  }

  response.writeHead(404).end("Not found");
}).listen(Number(process.env.PORT || 3000), "0.0.0.0");
