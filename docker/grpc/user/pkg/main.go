package main

import (
	"context"
	"database/sql"
    "fmt"
	"github.com/rs/zerolog/log"
	"net"
    "os"
	"time"

	_ "modernc.org/sqlite"
	"google.golang.org/grpc"
	pb "github.com/fcracker79/k8s-experiment/docker/grpc/user/proto"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/credentials/insecure"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
)
 
type server struct {
	pb.UnimplementedUserServiceServer
	db *sql.DB
}

func initConn() (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(os.Getenv("GRPC_OTEL_EXPORTER_OTLP_ENDPOINT"),
		// Note the use of insecure transport here. TLS is recommended in production.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to collector: %w", err)
	}

	return conn, err
}

func initTracerProvider(conn *grpc.ClientConn) (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	res, err := resource.New(ctx,
		resource.WithAttributes(
			// The service name used to display traces in backends
			semconv.ServiceNameKey.String("UserService"),
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
	return tracerProvider, nil
}

func (s *server) CreateUser(ctx context.Context, in *pb.User) (*pb.User, error) {
	stmt, err := s.db.Prepare("INSERT INTO users(id, name, description, created_at, updated_at) VALUES(?,?,?,?,?)")
	if err != nil {
		return nil, err
	}
	_, err = stmt.Exec(in.Id, in.Name, in.Description, in.CreatedAt, in.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return in, nil
}

func (s *server) GetUser(ctx context.Context, in *pb.User) (*pb.User, error) {
	row := s.db.QueryRow("SELECT name, description, created_at, updated_at FROM users WHERE id = ?", in.Id)
	var name, description, createdAt, updatedAt string
	err := row.Scan(&name, &description, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	return &pb.User{Id: in.Id, Name: name, Description: description, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
}

func (s *server) UpdateUser(ctx context.Context, in *pb.User) (*pb.User, error) {
	stmt, err := s.db.Prepare("UPDATE users SET name = ?, description = ?, updated_at = ? WHERE id = ?")
	if err != nil {
		return nil, err
	}
	_, err = stmt.Exec(in.Name, in.Description, time.Now().Format(time.RFC3339), in.Id)
	if err != nil {
		return nil, err
	}
	return in, nil
}

func (s *server) DeleteUser(ctx context.Context, in *pb.User) (*pb.User, error) {
	stmt, err := s.db.Prepare("DELETE FROM users WHERE id = ?")
	if err != nil {
		return nil, err
	}
	_, err = stmt.Exec(in.Id)
	if err != nil {
		return nil, err
	}
	return in, nil
}

func getEnvString(env string) string {
    if envVar, exists := os.LookupEnv(env); exists {
        return envVar
    } else {
        panic(fmt.Sprintf("%s not set", env))
    }
}

func CreateTable(db *sql.DB) {
    sql_table := `
    CREATE TABLE IF NOT EXISTS users(
        id TEXT NOT NULL PRIMARY KEY,
        name TEXT,
        description TEXT,
        created_at TIMESTAMP,
        updated_at TIMESTAMP);
    `

    _, err := db.Exec(sql_table)
    if err != nil {
        panic(err)
    }
}

func main() {
	otelCon, err := initConn()
	if err != nil {
		log.Fatal().Err(err).Msg("could not initialize tracer")
	}
	tracerProvider, err := initTracerProvider(otelCon)
	if err != nil {
		log.Fatal().Err(err).Msg("could not initialize tracer")
	}
	defer func() {
		if err := tracerProvider.Shutdown(context.Background()); err != nil {
			log.Fatal().Err(err).Msg("Failed to shutdown TracerProvider")
		}
	}()

	db, err := sql.Open("sqlite", "./user.db")
	if err != nil {
		log.Fatal().Msgf("failed to open database: %v", err)
	}
	defer db.Close()
    CreateTable(db)

    tcpPort := getEnvString("TCP_PORT")
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", tcpPort))
	if err != nil {
		log.Fatal().Msgf("failed to listen: %v", err)
	}
	
	s := grpc.NewServer(
		grpc.StatsHandler(
			otelgrpc.NewServerHandler(
				otelgrpc.WithTracerProvider(tracerProvider),
			),
		),
		grpc.UnaryInterceptor(serverLoggingInterceptor))
	pb.RegisterUserServiceServer(s, &server{db: db})

	log.Printf("Server listening on port %s", tcpPort)
	if err := s.Serve(lis); err != nil {
		log.Fatal().Msgf("failed to serve: %v", err)
	}
}

func serverLoggingInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	span := trace.SpanFromContext(ctx)
		
	traceID, err := span.SpanContext().TraceID().MarshalJSON()
	if err != nil {
		log.Fatal().Err(err).Msg("could not marshal traceID")
	}

	spanID, err := span.SpanContext().SpanID().MarshalJSON()
	if err != nil {
		log.Fatal().Err(err).Msg("could not marshal spanID")
	}

	log := zerolog.New(os.Stderr).With().Timestamp().
		Str("traceId", string(traceID)).
		Str("spanId", string(spanID)).
		Logger()
	log.Info().Msg("Request received")
	return handler(log.WithContext(ctx), req)
}
