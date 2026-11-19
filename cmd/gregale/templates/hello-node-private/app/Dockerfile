FROM node:22-alpine3.23 AS runtime

FROM alpine:3.23
RUN apk add --no-cache libatomic libstdc++
COPY --from=runtime /usr/local/bin/node /usr/local/bin/node
WORKDIR /app
COPY server.js ./
ENV PORT=3000
EXPOSE 3000
CMD ["node", "server.js"]
