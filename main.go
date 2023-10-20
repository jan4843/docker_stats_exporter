package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type exporter struct {
	docker      *client.Client
	extraLabels map[string]*template.Template
}

func (e *exporter) Describe(ch chan<- *prometheus.Desc) {
	// validate user-provided labels on a dummy metric
	labels := []string{}
	for label := range e.extraLabels {
		labels = append(labels, label)
	}
	ch <- prometheus.NewDesc("validate", "", labels, nil)
}

func (e *exporter) Collect(ch chan<- prometheus.Metric) {
	containers, err := e.docker.ContainerList(
		context.TODO(),
		types.ContainerListOptions{All: true},
	)
	if err != nil {
		log.Fatalf("cannot list containers: %v", err)
		return
	}

	var wg sync.WaitGroup
	for _, container := range containers {
		container := container
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := e.collectContainer(&container, ch)
			if err != nil {
				log.Printf("cannot collect container %s: %v", container.ID, err)
			}
		}()
	}
	wg.Wait()
}

func (e *exporter) collectContainer(container *types.Container, ch chan<- prometheus.Metric) error {
	containerJson, err := e.docker.ContainerInspect(context.TODO(), container.ID)
	if err != nil {
		return err
	}

	labelsNames := []string{"name"}
	labelsValues := []string{strings.Trim(container.Names[0], "/")}
	for labelName, labelTemplate := range e.extraLabels {
		templateData := struct {
			Container     *types.Container
			ContainerJSON types.ContainerJSON
		}{
			container,
			containerJson,
		}
		var labelValue bytes.Buffer
		labelTemplate.Execute(&labelValue, templateData)
		labelsNames = append(labelsNames, labelName)
		labelsValues = append(labelsValues, labelValue.String())
	}

	// Info
	ch <- prometheus.MustNewConstMetric(prometheus.NewDesc(
		"docker_container_info", "",
		labelsNames, nil),
		prometheus.GaugeValue,
		1,
		labelsValues...)

	if container.State != "running" {
		return nil
	}

	var stats types.StatsJSON
	statsReader, err := e.docker.ContainerStatsOneShot(context.TODO(), container.ID)
	if err != nil {
		return fmt.Errorf("cannot get stats: %v", err)
	}
	defer statsReader.Body.Close()
	err = json.NewDecoder(statsReader.Body).Decode(&stats)
	if err != nil {
		return fmt.Errorf("cannot decode stats: %v", err)
	}

	// CPU
	ch <- prometheus.MustNewConstMetric(prometheus.NewDesc(
		"docker_container_cpu_seconds_total", "",
		labelsNames, nil),
		prometheus.CounterValue,
		nsToS(stats.CPUStats.CPUUsage.TotalUsage),
		labelsValues...)

	// Memory
	{
		// https://github.com/docker/docker-ce/blob/6bb4de18c8cdca6916074d7a0be640e27c689202/components/cli/cli/command/container/stats_helpers.go#L227-L249
		memoryBytes := stats.MemoryStats.Usage
		cacheKey := "total_inactive_file"
		if _, isCgroupV1 := stats.MemoryStats.Stats["total_inactive_file"]; !isCgroupV1 {
			cacheKey = "inactive_file"
		}
		if cacheBytes, ok := stats.MemoryStats.Stats[cacheKey]; ok {
			if memoryBytes < cacheBytes {
				memoryBytes = 0
			} else {
				memoryBytes -= cacheBytes
			}
		}

		ch <- prometheus.MustNewConstMetric(prometheus.NewDesc(
			"docker_container_memory_usage_bytes", "",
			labelsNames, nil),
			prometheus.GaugeValue,
			float64(memoryBytes),
			labelsValues...)

		ch <- prometheus.MustNewConstMetric(prometheus.NewDesc(
			"docker_container_memory_limit_bytes", "",
			labelsNames, nil),
			prometheus.GaugeValue,
			float64(stats.MemoryStats.Limit),
			labelsValues...)
	}

	// Network
	{
		var rxBytes, txBytes uint64
		for _, network := range stats.Networks {
			rxBytes += network.RxBytes
			txBytes += network.TxBytes
		}

		ch <- prometheus.MustNewConstMetric(prometheus.NewDesc(
			"docker_container_network_rx_bytes_total", "",
			labelsNames, nil),
			prometheus.CounterValue,
			float64(rxBytes),
			labelsValues...)

		ch <- prometheus.MustNewConstMetric(prometheus.NewDesc(
			"docker_container_network_tx_bytes_total", "",
			labelsNames, nil),
			prometheus.CounterValue,
			float64(txBytes),
			labelsValues...)
	}

	// Block I/O
	{
		var readBytes, writeBytes uint64
		for _, blkioStat := range stats.BlkioStats.IoServiceBytesRecursive {
			switch blkioStat.Op {
			case "read":
				readBytes += blkioStat.Value
			case "write":
				writeBytes += blkioStat.Value
			}
		}

		ch <- prometheus.MustNewConstMetric(prometheus.NewDesc(
			"docker_container_blkio_read_bytes_total", "",
			labelsNames, nil),
			prometheus.CounterValue,
			float64(readBytes),
			labelsValues...)

		ch <- prometheus.MustNewConstMetric(prometheus.NewDesc(
			"docker_container_blkio_write_bytes_total", "",
			labelsNames, nil),
			prometheus.CounterValue,
			float64(writeBytes),
			labelsValues...)
	}

	// PIDs
	ch <- prometheus.MustNewConstMetric(prometheus.NewDesc(
		"docker_container_pids", "",
		labelsNames, nil),
		prometheus.GaugeValue,
		float64(stats.PidsStats.Current),
		labelsValues...)

	return nil
}

func nsToS(ns uint64) float64 {
	return float64(ns) / float64(time.Second)
}

func main() {
	extraLabels := make(map[string]*template.Template)
	envPrefix := "LABEL_"
	for _, env := range os.Environ() {
		name, value, _ := strings.Cut(env, "=")
		if strings.HasPrefix(name, envPrefix) {
			label := strings.TrimPrefix(name, envPrefix)
			tmpl, err := template.New(label).Parse(value)
			if err != nil {
				log.Fatalf("invalid template for label %s: %v", label, err)
			}
			extraLabels[label] = tmpl
		}
	}

	addr := ":9338"
	if os.Getenv("ADDR") != "" {
		addr = os.Getenv("ADDR")
	}

	docker, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		log.Fatalf("cannot create docker client: %v", err)
		return
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(&exporter{
		docker:      docker,
		extraLabels: extraLabels,
	})
	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	http.Handle("/metrics", handler)
	http.Handle("/", http.RedirectHandler("/metrics", http.StatusMovedPermanently))
	fmt.Printf("Listening on http://%s...\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
