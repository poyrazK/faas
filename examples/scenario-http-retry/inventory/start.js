import http from "node:http";
http.createServer((_request, response) => {
  response.writeHead(200, { "content-type": "application/json" });
  response.end(JSON.stringify({ available: true }));
}).listen(Number(process.env.PORT || 8080), process.env.HOST || "0.0.0.0");
