package otel

import (
	"context"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// mockExporter is a simple mock implementation of sdktrace.SpanExporter
type mockExporter struct {
	RecordedSpans []sdktrace.ReadOnlySpan
}

// buildMockExporter creates a mock exporter for testing purposes.
func (m *mockExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	// Mock implementation: just log the spans to stdout
	for _, span := range spans {
		m.RecordedSpans = append(m.RecordedSpans, span)
	}
	return nil
}

func (m *mockExporter) Shutdown(ctx context.Context) error {
	// Mock shutdown does nothing
	return nil
}
