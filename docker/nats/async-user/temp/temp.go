package main

import (
	"context"
	"fmt"
    "go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	header := map[string][]string{
		"Traceparent": {"00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"},
	}
	ctx := otel.GetTextMapPropagator().Extract(
		context.Background(), propagation.HeaderCarrier(header))
		span := trace.SpanFromContext(ctx)
		fmt.Println(span.SpanContext().TraceID())
}
