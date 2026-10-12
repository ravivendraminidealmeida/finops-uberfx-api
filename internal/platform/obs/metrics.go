package obs

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	Results              *prometheus.CounterVec
	Duplicates           prometheus.Counter
	Retries              prometheus.Counter
	DLQ                  prometheus.Counter
	Conflicts            prometheus.Counter
	OutboxLagSeconds     prometheus.Gauge
	ProcessingSeconds    prometheus.Histogram
	ReconcileDivergences prometheus.Counter
}

func NewMetrics() *Metrics {
	m := &Metrics{
		Results: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "finops_wager_results_total",
			Help: "Wager processing results by status",
		}, []string{"status", "kind"}),
		Duplicates: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "finops_wager_duplicates_total",
			Help: "Idempotent replays",
		}),
		Retries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "finops_wager_retries_total",
			Help: "Transient SQS handle failures left for redelivery",
		}),
		DLQ: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "finops_wager_dlq_candidates_total",
			Help: "Messages not deleted (invalid or permanent errors) destined for redrive/DLQ",
		}),
		Conflicts: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "finops_wager_conflicts_total",
			Help: "Idempotency and uniqueness conflicts",
		}),
		OutboxLagSeconds: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "finops_outbox_lag_seconds",
			Help: "Age of the oldest claimed unpublished outbox record",
		}),
		ProcessingSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "finops_wager_processing_seconds",
			Help:    "ProcessWager latency",
			Buckets: prometheus.DefBuckets,
		}),
		ReconcileDivergences: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "finops_reconciliation_divergences_total",
			Help: "Wallet reconciliation mismatches",
		}),
	}
	_ = prometheus.Register(m.Results)
	_ = prometheus.Register(m.Duplicates)
	_ = prometheus.Register(m.Retries)
	_ = prometheus.Register(m.DLQ)
	_ = prometheus.Register(m.Conflicts)
	_ = prometheus.Register(m.OutboxLagSeconds)
	_ = prometheus.Register(m.ProcessingSeconds)
	_ = prometheus.Register(m.ReconcileDivergences)
	return m
}
