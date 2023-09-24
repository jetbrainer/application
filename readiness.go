package app

import (
	"net/http"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
)

type ReadinessHandler struct {
	areReady []*atomic.Value
}

func NewReadinessHandler(areReady ...*atomic.Value) ReadinessHandler {
	return ReadinessHandler{
		areReady: areReady,
	}
}

func (h ReadinessHandler) Register(r chi.Router) {
	r.Get("/ready", ready(h.areReady))
	r.Get("/health/ready", ready(h.areReady))
}

func ready(areReady []*atomic.Value) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		for _, isReady := range areReady {
			if isReady == nil || !isReady.Load().(bool) {
				AnswerWithJSONError(w, http.StatusServiceUnavailable)
				return
			}
		}

		w.WriteHeader(http.StatusOK)
	}
}
