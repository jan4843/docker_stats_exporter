FROM golang:1.20 AS builder
WORKDIR /build
COPY . ./
RUN CGO_ENABLED=0 go build

FROM scratch
COPY --from=builder /build/docker_stats_exporter /docker_stats_exporter
ENTRYPOINT ["/docker_stats_exporter"]
