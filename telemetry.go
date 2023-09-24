package app

import (
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type TelemetryHandler struct {
	prometheusRegistry *prometheus.Registry
}

func NewTelemtryHandler(prometheusRegistry *prometheus.Registry) TelemetryHandler {
	return TelemetryHandler{
		prometheusRegistry: prometheusRegistry}
}

func (h TelemetryHandler) Register(r chi.Router) {
	prometheusHandler := promhttp.InstrumentMetricHandler(
		h.prometheusRegistry, promhttp.HandlerFor(h.prometheusRegistry, promhttp.HandlerOpts{}),
	)
	r.Get("/metrics", prometheusHandler.ServeHTTP)
}
