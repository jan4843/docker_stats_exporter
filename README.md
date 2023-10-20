# Docker Stats Exporter

Prometheus exporter for Docker containers resources usage.

The values exported reflect the output of the [`docker stats` command](https://docs.docker.com/engine/reference/commandline/stats/).

## Usage

The exporter can be started without configuration and metrics will be exposed at http://0.0.0.0:9338/metrics.

```console
$ docker_stats_exporter
Listening on http://:9338...
```

### Docker Compose

```yaml
services:
  docker_stats_exporter:
    build: https://github.com/jan4843/docker_stats_exporter.git
    environment:
      LABEL_state: '{{.Container.State}}'
      LABEL_health: '{{.ContainerJSON.State.Health.Status}}'
      LABEL_compose_project: '{{index .Container.Labels "com.docker.compose.project"}}'
    ports:
      - 9338:9338
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
```

## Configuration

### Docker Host

By default, metrics are retrieved from the Docker socket at `/var/run/docker.sock`, but a different Docker Engine context can be configured via environmental variables such as `DOCKER_HOST` as explained in the [Docker documentation](https://docs.docker.com/desktop/faqs/general/#how-do-i-connect-to-the-remote-docker-engine-api).

### Custom Metric Labels

The only label exposed for all metrics is `name`, the container name.

To expose additional labels, environmental variables with a `LABEL_` prefix are used. The environmental variable name (excluding the prefix) is used as the metric name, and its value [Go-templated](https://pkg.go.dev/text/template) with [`Container` struct](https://pkg.go.dev/github.com/docker/docker/api/types#Container) and [`ContainerJSON` struct](https://pkg.go.dev/github.com/docker/docker/api/types#ContainerJSON) variables in scope.

See the Docker Compose example above adding the `state`, `health`, and `compose_project` metric labels.

## Metrics

The metric `docker_container_info` is available for all containers, including non-running ones, and always has a static value of 1.

```ini
# TYPE docker_container_info gauge
docker_container_info{name="nginx"} 1
docker_container_info{name="redis"} 1

# TYPE docker_container_cpu_seconds_total counter
docker_container_cpu_seconds_total{name="nginx"} 0.138186

# TYPE docker_container_memory_usage_bytes gauge
docker_container_memory_usage_bytes{name="nginx"} 4.28032e+06

# TYPE docker_container_memory_limit_bytes gauge
docker_container_memory_limit_bytes{name="nginx"} 3.521634304e+09

# TYPE docker_container_network_rx_bytes_total counter
docker_container_network_rx_bytes_total{name="nginx"} 6062

# TYPE docker_container_network_tx_bytes_total counter
docker_container_network_tx_bytes_total{name="nginx"} 9047

# TYPE docker_container_blkio_read_bytes_total counter
docker_container_blkio_read_bytes_total{name="nginx"} 77824

# TYPE docker_container_blkio_write_bytes_total counter
docker_container_blkio_write_bytes_total{name="nginx"} 8192

# TYPE docker_container_pids gauge
docker_container_pids{name="nginx"} 5
```
