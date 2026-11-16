# Build from the repository root:
# docker build -f apps/server/deploy/Dockerfile -t serversend:dev .
FROM node:26-bookworm-slim AS admin-build
WORKDIR /admin
COPY apps/server/admin/package.json apps/server/admin/package-lock.json ./
RUN npm ci
COPY apps/server/admin/index.html apps/server/admin/tsconfig.json apps/server/admin/vite.config.ts ./
COPY apps/server/admin/src ./src
COPY apps/server/admin/public ./public
COPY apps/server/admin/checks ./checks
COPY apps/server/admin/scripts ./scripts
RUN npm run build

FROM golang:1.26.3-bookworm AS build
WORKDIR /src
ENV GOWORK=off CGO_ENABLED=1
COPY shared/go.mod shared/go.sum ./shared/
COPY apps/server/go.mod apps/server/go.sum ./apps/server/
WORKDIR /src/apps/server
RUN go mod download
WORKDIR /src
COPY shared/api ./shared/api
COPY shared/protocol ./shared/protocol
COPY shared/sqlite ./shared/sqlite
COPY apps/server/cmd ./apps/server/cmd
COPY apps/server/internal ./apps/server/internal
COPY apps/server/admin/assets.go ./apps/server/admin/assets.go
COPY --from=admin-build /admin/dist ./apps/server/admin/dist
WORKDIR /src/apps/server
RUN go build -mod=readonly -trimpath -ldflags='-s -w' -o /out/serversend ./cmd/serversend

FROM debian:bookworm-slim
RUN mkdir /config /data /staging \
    && chown 10001:10001 /config /data /staging \
    && chmod 0700 /config /data /staging
COPY --from=build /out/serversend /usr/local/bin/serversend
USER 10001:10001
VOLUME ["/config", "/data", "/staging"]
EXPOSE 8443/tcp
# Reserved for the later QUIC listener; currently no UDP service is started.
EXPOSE 8443/udp
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/serversend"]
