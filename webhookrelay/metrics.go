package webhookrelay

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// Delivery results, the `result` label of webhook_relay_deliveries_total.
const (
	ResultForwarded      = "forwarded"
	ResultPing           = "ping"
	ResultUnauthorized   = "unauthorized"
	ResultForbiddenOwner = "forbidden_owner"
	ResultTooLarge       = "too_large"
	ResultBadRequest     = "bad_request"
	ResultTargetError    = "target_error"
)

var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

// metrics is the Prometheus text exposition of the relay, hand-written so the
// binary carries no client library for two series.
type metrics struct {
	mu         sync.Mutex
	deliveries map[[3]string]uint64
	counts     []uint64 // per bucket, not cumulative
	inf        uint64
	sum        float64
}

func newMetrics() *metrics {
	return &metrics{deliveries: map[[3]string]uint64{}, counts: make([]uint64, len(durationBuckets))}
}

func (m *metrics) delivery(sender, event, result string) {
	m.mu.Lock()
	m.deliveries[[3]string{sender, event, result}]++
	m.mu.Unlock()
}

func (m *metrics) forward(seconds float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sum += seconds
	m.inf++

	for i, b := range durationBuckets {
		if seconds <= b {
			m.counts[i]++

			return
		}
	}
}

func (m *metrics) write(out io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var w strings.Builder

	fmt.Fprintln(&w, "# HELP webhook_relay_deliveries_total Webhook deliveries received, by verified sender, event and outcome.")
	fmt.Fprintln(&w, "# TYPE webhook_relay_deliveries_total counter")

	keys := make([][3]string, 0, len(m.deliveries))
	for k := range m.deliveries {
		keys = append(keys, k)
	}

	sort.Slice(keys, func(i, j int) bool {
		for x := range 3 {
			if keys[i][x] != keys[j][x] {
				return keys[i][x] < keys[j][x]
			}
		}

		return false
	})

	for _, k := range keys {
		fmt.Fprintf(&w, "webhook_relay_deliveries_total{sender=%q,event=%q,result=%q} %d\n", k[0], k[1], k[2], m.deliveries[k])
	}

	fmt.Fprintln(&w, "# HELP webhook_relay_forward_duration_seconds Time to forward a delivery to the target.")
	fmt.Fprintln(&w, "# TYPE webhook_relay_forward_duration_seconds histogram")

	var cum uint64

	for i, b := range durationBuckets {
		cum += m.counts[i]
		fmt.Fprintf(&w, "webhook_relay_forward_duration_seconds_bucket{le=\"%g\"} %d\n", b, cum)
	}

	fmt.Fprintf(&w, "webhook_relay_forward_duration_seconds_bucket{le=\"+Inf\"} %d\n", m.inf)
	fmt.Fprintf(&w, "webhook_relay_forward_duration_seconds_sum %g\n", m.sum)
	fmt.Fprintf(&w, "webhook_relay_forward_duration_seconds_count %d\n", m.inf)

	_, _ = io.WriteString(out, w.String())
}
