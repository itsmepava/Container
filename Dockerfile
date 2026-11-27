# Eimsky server: one static binary serving the web app, the mobile API and background jobs.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/eimsky ./cmd/eimsky

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 eimsky && mkdir -p /data && chown eimsky /data
COPY --from=build /out/eimsky /usr/local/bin/eimsky
USER eimsky
ENV ADDR=:8080 DATA_DIR=/data
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://localhost:8080/healthz || exit 1
ENTRYPOINT ["eimsky"]
CMD ["serve"]
