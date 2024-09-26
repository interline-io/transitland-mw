package local

import (
	"net/http"

	"github.com/interline-io/transitland-mw/metrics"
)

type DefaultMetric struct{}

func NewDefaultMetric() *DefaultMetric {
	return &DefaultMetric{}
}

func (m *DefaultMetric) NewJobMetric(queue string) metrics.JobMetric {
	return &DefaultMetric{}
}

func (m *DefaultMetric) NewApiMetric(handlerName string) metrics.ApiMetric {
	return &DefaultMetric{}
}

func (m *DefaultMetric) MetricsHandler() http.Handler {
	return nil
}

func (m *DefaultMetric) AddStartedJob(queueName string, jobType string) {
}

func (m *DefaultMetric) AddCompletedJob(queueName string, jobType string, success bool) {
}

func (m *DefaultMetric) AddResponse(method string, responseCode int, requestSize int64, responseSize int64, responseTime float64) {
}
