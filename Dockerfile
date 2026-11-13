# syntax=docker/dockerfile:1
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# modernc.org/sqlite 为纯 Go 实现，可关闭 CGO 产出静态二进制。
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/server /app/server
ENV PORT=8080 \
    DB_PATH=/data/app.db \
    SEED=1
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/server"]
