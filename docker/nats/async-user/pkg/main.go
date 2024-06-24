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
	ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(msg.Header))
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	logger := zerolog.Ctx(ctx)
	span := trace.SpanFromContext(ctx)
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
