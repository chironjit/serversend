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
COPY shared ./shared
COPY apps/server/cmd ./apps/server/cmd
COPY apps/server/internal ./apps/server/internal
COPY apps/server/admin/assets.go ./apps/server/admin/assets.go
COPY --from=admin-build /admin/dist ./apps/server/admin/dist
WORKDIR /src/apps/server
RUN go build -mod=readonly -trimpath -ldflags='-s -w' -o /out/serversend ./cmd/serversend

FROM debian:bookworm-slim
# SQLite spills large temporary tables/sorts here with a read-only root.
ENV SQLITE_TMPDIR=/staging
# /backups is an optional mount for automatic pre-upgrade backups; it is not a
# VOLUME, so an unmounted path never silently keeps backups inside Docker.
RUN mkdir /config /data /staging /backups \
    && chown 10001:10001 /config /data /staging /backups \
    && chmod 0700 /config /data /staging /backups
COPY --from=build /out/serversend /usr/local/bin/serversend
USER 10001:10001
VOLUME ["/config", "/data", "/staging"]
EXPOSE 58443/tcp
# HTTP/3 (QUIC); clients fall back to HTTP/2 over TCP when UDP is blocked.
EXPOSE 58443/udp
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/serversend"]
