// Package trace provides support for tracing operations.
package trace

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/onflow/rosetta/log"
	"github.com/onflow/rosetta/process"
	"github.com/onflow/rosetta/version"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/global"
	"go.opentelemetry.io/otel/metric/instrument/asyncint64"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	controller "go.opentelemetry.io/otel/sdk/metric/controller/basic"
	"go.opentelemetry.io/otel/sdk/metric/export/aggregation"
	processor "go.opentelemetry.io/otel/sdk/metric/processor/basic"
	selector "go.opentelemetry.io/otel/sdk/metric/selector/simple"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	tracerName = "rosetta/flow"
)

// Re-export useful functions from the the otel trace package.
var (
	Bool            = attribute.Bool
	Int             = attribute.Int
	Int64           = attribute.Int64
	Float64         = attribute.Float64
	SpanFromContext = trace.SpanFromContext
	String          = attribute.String
	Stringer        = attribute.Stringer
)

var (
	meterOpts = []metric.MeterOption{
		metric.WithInstrumentationVersion(version.FlowRosetta),
		metric.WithSchemaURL(semconv.SchemaURL),
	}
	root = otel.Tracer(tracerName)
)

// Span is an alias for the opentelemetry trace.Span type.
type Span = trace.Span

// KeyValue is an alias for the opentelemetry attribute.KeyValue type.
type KeyValue = attribute.KeyValue

// Counter creates an instrument for recording increasing values.
func Counter(namespace string, name string) syncint64.Counter {
	meter := global.MeterProvider().Meter("rosetta.flow."+namespace, meterOpts...)
	c, err := meter.SyncInt64().Counter(name)
	if err != nil {
		log.Fatalf("Failed to instantiate counter %s.%s: %s", namespace, name, err)
	}
	return c
}

// EndSpanErr ends the span with an error status containing the given error as
// its description.
func EndSpanErr(span trace.Span, err error) {
	msg := err.Error()
	span.AddEvent("error_log", trace.WithAttributes(attribute.String("error_message", msg)))
	span.SetStatus(codes.Error, msg)
	span.End()
}

// EndSpanErrorf ends the span with an error status containing the given
// printf-formatted description.
func EndSpanErrorf(span trace.Span, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	span.AddEvent("error_log", trace.WithAttributes(attribute.String("error_message", msg)))
	span.SetStatus(codes.Error, msg)
	span.End()
}

// EndSpanOk ends the span with an ok status.
func EndSpanOk(span trace.Span) {
	span.SetStatus(codes.Ok, "")
	span.End()
}

// Gauge creates an instrument for recording the current value.
func Gauge(namespace string, name string) asyncint64.Gauge {
	meter := global.MeterProvider().Meter("rosetta.flow."+namespace, meterOpts...)
	c, err := meter.AsyncInt64().Gauge(name)
	if err != nil {
		log.Fatalf("Failed to instantiate guage %s.%s: %s", namespace, name, err)
	}
	return c
}

// GetMethodName returns the method name from a fully qualified gRPC method
// value.
func GetMethodName(method string) string {
	split := strings.Split(method, "/")
	return split[len(split)-1]
}

// Hexify returns a byte slice encoded in hex for trace attributes.
func Hexify(key string, val []byte) attribute.KeyValue {
	return attribute.String(key, hex.EncodeToString(val))
}

// Histogram creates an instrument for recording a distribution of values.
func Histogram(namespace string, name string) syncint64.Histogram {
	meter := global.MeterProvider().Meter("rosetta.flow."+namespace, meterOpts...)
	c, err := meter.SyncInt64().Histogram(name)
	if err != nil {
		log.Fatalf("Failed to instantiate histogram %s.%s: %s", namespace, name, err)
	}
	return c
}

// Init initializes the global tracer provider with an OTLP (OpenTelemetry
// Protocol) exporter.
func Init(ctx context.Context) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return
	}
	// Configure the resource to automatically pull attributes from environment
	// variables like OTEL_RESOURCE_ATTRIBUTES and OTEL_SERVICE_NAME.
	res, err := resource.New(
		context.Background(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String("rosetta/flow"),
		),
		resource.WithFromEnv(),
		resource.WithProcess(),
	)
	if err != nil {
		log.Fatalf("Failed to create OpenTelemetry resource: %s", err)
	}
	traceClient := otlptracegrpc.NewClient()
	traceExporter, err := otlptrace.New(ctx, traceClient)
	if err != nil {
		log.Fatalf("Failed to create OTLP trace exporter: %s", err)
	}
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	process.SetExitHandler(func() {
		if err := tracerProvider.Shutdown(ctx); err != nil {
			log.Fatalf("Failed to stop tracer provider: %s", err)
		}
	})
	otel.SetTracerProvider(tracerProvider)
	root = tracerProvider.Tracer(tracerName)
	metricClient := otlpmetricgrpc.NewClient()
	metricExporter, err := otlpmetric.New(ctx, metricClient)
	if err != nil {
		log.Fatalf("Failed to create OTLP metric exporter: %s", err)
	}
	meterProvider := controller.New(
		processor.NewFactory(
			selector.NewWithHistogramDistribution(),
			aggregation.StatelessTemporalitySelector(),
		),
		controller.WithExporter(metricExporter),
		controller.WithResource(res),
	)
	err = meterProvider.Start(ctx)
	if err != nil {
		log.Fatalf("Failed to start meter provider: %s", err)
	}
	process.SetExitHandler(func() {
		if err := meterProvider.Stop(ctx); err != nil {
			log.Fatalf("Failed to stop meter provider: %s", err)
		}
	})
	global.SetMeterProvider(meterProvider)
}

// NewSpan returns a new span for the given context.
func NewSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return root.Start(ctx, name, trace.WithAttributes(attrs...))
}

// SetAttributes sets the given attributes on the span for the given context.
func SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	trace.SpanFromContext(ctx).SetAttributes(attrs...)
}

// Uint64 returns a uint64 encoded as a string for trace attributes.
func Uint64(key string, val uint64) attribute.KeyValue {
	return attribute.String(key, strconv.FormatUint(val, 10))
}

// UpDownCounter creates an instrument for recording changes of a value.
func UpDownCounter(namespace string, name string) syncint64.UpDownCounter {
	meter := global.MeterProvider().Meter("rosetta.flow."+namespace, meterOpts...)
	c, err := meter.SyncInt64().UpDownCounter(name)
	if err != nil {
		log.Fatalf("Failed to instantiate up/down counter %s.%s: %s", namespace, name, err)
	}
	return c
}
