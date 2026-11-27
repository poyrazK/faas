package main

import (
	"github.com/prometheus/client_golang/prometheus"
)

// jailFSStat reports a filesystem's size and free bytes; ok is false when it
// could not be read.
type jailFSStat func(path string) (size, avail uint64, ok bool)

// registerJailTmpfsMetrics exports the jailer chroot tmpfs's size and free
// bytes. The tmpfs is root-owned with mode 1700, so the unprivileged
// node_exporter cannot statfs it (it reports device_error "permission
// denied" and no size); vmmd is root and reads it at scrape time. A statfs is
// cheap and never blocks, so no sampler is needed. A failed read exports no
// sample rather than zero, so the operator disk pressure report
// (ADR-20261124150000) shows the jail as unread, not empty.
func registerJailTmpfsMetrics(reg prometheus.Registerer, path string, stat jailFSStat) {
	if stat == nil {
		stat = statJailFS
	}
	reg.MustRegister(&jailTmpfsCollector{
		path: path,
		stat: stat,
		size: prometheus.NewDesc("vmmd_jail_tmpfs_size_bytes",
			"Size of the jailer chroot tmpfs (/srv/fc/jail) on this compute host. It is RAM, held until the files are removed or the host reboots.", nil, nil),
		avail: prometheus.NewDesc("vmmd_jail_tmpfs_avail_bytes",
			"Free bytes in the jailer chroot tmpfs (/srv/fc/jail) on this compute host.", nil, nil),
	})
}

type jailTmpfsCollector struct {
	path        string
	stat        jailFSStat
	size, avail *prometheus.Desc
}

func (c *jailTmpfsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.size
	ch <- c.avail
}

func (c *jailTmpfsCollector) Collect(ch chan<- prometheus.Metric) {
	size, avail, ok := c.stat(c.path)
	if !ok || size == 0 {
		return
	}
	ch <- prometheus.MustNewConstMetric(c.size, prometheus.GaugeValue, float64(size))
	ch <- prometheus.MustNewConstMetric(c.avail, prometheus.GaugeValue, float64(avail))
}
