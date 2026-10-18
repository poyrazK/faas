import http from "node:http";

http.createServer((_request, response) => {
  response.writeHead(200, { "content-type": "application/json" });
  response.end('{"ready":true}');
}).listen(Number(process.env.PORT || 8080), "0.0.0.0");
