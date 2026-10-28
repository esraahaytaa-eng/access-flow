# One multi-stage build (A2): the React build is embedded in the Go binary.
# Targets: "app" (AccessFlow, port 8080) and "mocktools" (Mock Tool Service).

FROM node:24.22.1-alpine3.24 AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.27.3-alpine3.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY --from=web /web/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/accessflow ./cmd/accessflow \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mocktools ./cmd/mocktools \
 && mkdir -m 0700 /out/appdata

FROM gcr.io/distroless/static-debian12:nonroot AS mocktools
COPY --from=build /out/mocktools /mocktools
EXPOSE 8081
ENTRYPOINT ["/mocktools"]

FROM gcr.io/distroless/static-debian12:nonroot AS app
COPY --from=build /out/accessflow /accessflow
# Holds the one-time bootstrap password file (A4); the appdata volume inherits this owner.
COPY --from=build --chown=65532:65532 /out/appdata /var/lib/accessflow
EXPOSE 8080
ENTRYPOINT ["/accessflow"]
