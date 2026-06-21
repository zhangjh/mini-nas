FROM node:20-alpine AS web-builder
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

FROM golang:1.22-alpine AS go-builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /build
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ .
COPY --from=web-builder /web/dist ./cmd/server/web
RUN CGO_ENABLED=0 go build -tags withweb -ldflags="-s -w" -o mini-nas-server ./cmd/server

FROM alpine:3.19
RUN apk add --no-cache ca-certificates ffmpeg tzdata
RUN adduser -D -h /data -s /sbin/nologin mini-nas
WORKDIR /app
COPY --from=go-builder /build/mini-nas-server .
RUN mkdir -p /data && chown -R mini-nas:mini-nas /data /app
USER mini-nas
EXPOSE 8080
VOLUME ["/data"]
CMD ["./mini-nas-server", "-config", "/data/config.json", "-data", "/data"]
