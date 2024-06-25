package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	pb "github.com/fcracker79/k8s-experiment/docker/nats/async_users/proto/user"
	nats "github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
)

const (
	natsUrl               = "NATS_URL"
	natsCreateUserSubject = "NATS_CREATE_USER_SUBJECT"
	grpcUserHost          = "GRPC_USER_HOST"

	natsDurableConsumerName = "asyncUserCreator"
)

func getEnvString(env string) string {
	if envVar, exists := os.LookupEnv(env); exists {
		return envVar
	} else {
		panic(fmt.Sprintf("%s not set", env))
	}
}

func createNATSConnection() (*nats.Conn, error) {
	return nats.Connect(getEnvString(natsUrl))
}

func main() {
	logger := zerolog.New(os.Stderr)
	ctx := logger.WithContext(context.Background())

	otelCon, err := initOtelConn()
	if err != nil {
		logger.Fatal().Err(err).Msg("could not initialize tracer")
	}
	shutdownTracerProvider, err := initTracerProvider(otelCon)
	if err != nil {
		logger.Fatal().Err(err).Msg("could not initialize tracer")
	}
	defer func() {
		if err := shutdownTracerProvider(context.Background()); err != nil {
			logger.Fatal().Err(err).Msg("Failed to shutdown TracerProvider")
		}
	}()
	// Connect to a NATS server
	nc, err := createNATSConnection()
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect to NATS")
	}
	defer nc.Close()

	subject := getEnvString(natsCreateUserSubject)
	// Subscribe to subject
	js, err := nc.JetStream(nats.PublishAsyncMaxPending(256))
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to connect to Jetstream")
	}

	sub, err := js.Subscribe(subject, 
		func(msg *nats.Msg){createUserFromMessage(ctx, msg)},
		nats.Durable(natsDurableConsumerName))
	if err != nil {
		logger.Fatal().Err(err).Msgf("failed to subscribe to NATS stream %s", subject)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	if err := sub.Unsubscribe(); err != nil {
		log.Fatal(err)
	}
}

func createUserFromMessage(ctx context.Context, msg *nats.Msg) {
	extractCtx := otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(msg.Header))
	ctx, cancel := context.WithTimeout(extractCtx, 30*time.Second)
	defer cancel()
	logger := zerolog.Ctx(ctx)
	logger.Info().Msgf("Received a message: %s, headers %v\n", string(msg.Data), msg.Header)
	span := trace.SpanFromContext(extractCtx)
	spanID, err := span.SpanContext().SpanID().MarshalJSON()	
	if err != nil {
		logger.Fatal().Err(err).Msg("could not marshal spanID")
	}
	traceID, err := span.SpanContext().TraceID().MarshalJSON()
	if err != nil {
		logger.Fatal().Err(err).Msg("could not marshal traceID")
	}
	
	loggerWithTrace := zerolog.Ctx(ctx).With().Str("traceId", string(traceID)).Str("spanId", string(spanID)).Logger()
	logger = &loggerWithTrace
	
	logger.Info().Msgf("Received a message: %s\n", string(msg.Data))
	var user pb.User
	err = json.Unmarshal(msg.Data, &user)
	if err != nil {
		logger.Fatal().Err(err).Msg("could not unmarshal request body")
	}
	connection, err := createGrpcConnection()
	if err != nil {
		logger.Fatal().Err(err).Msg("did not connect")
	}
	defer connection.Close()
	c := pb.NewUserServiceClient(connection)
	createdUser, err := c.CreateUser(ctx, &user)
	if err != nil {
		logger.Fatal().Err(err).Msg("could not create user")
	}
	logger.Info().Msgf("User created: %v", createdUser)
}

func createGrpcConnection() (*grpc.ClientConn, error) {
	connection, err := grpc.NewClient(
		getUserGrpcEndpoint(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
	)
	return connection, err
}

func getUserGrpcEndpoint() string {
	return fmt.Sprintf("%s:%s", getEnvString(grpcUserHost), getEnvString("GRPC_USER_PORT"))
}

func initTracerProvider(conn *grpc.ClientConn) (func(context.Context) error, error) {
	ctx := context.Background()
	res, err := resource.New(ctx,
		resource.WithAttributes(
			// The service name used to display traces in backends
			semconv.ServiceNameKey.String("APIGWService"),
		),
	)
	if err != nil {
		return nil, err
	}
	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	// Register the trace exporter with a TracerProvider, using a batch
	// span processor to aggregate spans before export.
	bsp := sdktrace.NewBatchSpanProcessor(traceExporter)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)
	otel.SetTracerProvider(tracerProvider)

	// Set global propagator to tracecontext (the default is no-op).
	otel.SetTextMapPropagator(propagation.TraceContext{})
	// Shutdown will flush any remaining spans and shut down the exporter.
	return tracerProvider.Shutdown, nil
}

func initOtelConn() (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(os.Getenv("GRPC_OTEL_EXPORTER_OTLP_ENDPOINT"),
		// Note the use of insecure transport here. TLS is recommended in production.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to collector: %w", err)
	}

	return conn, err
}