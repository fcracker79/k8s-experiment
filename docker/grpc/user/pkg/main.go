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
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.20.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
)
 
type server struct {
	pb.UnimplementedUserServiceServer
	db *sql.DB
}

func initTracer() (*sdktrace.TracerProvider, error) {
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
		otlptracehttp.WithHeaders(map[string]string{"X-ServiceName": "apigw-service"}),
		otlptracehttp.WithTimeout(30 * time.Second),
	}
	opts = append(opts, otlptracehttp.WithInsecure())
	client := otlptracehttp.NewClient(opts...)

	exporter, err := otlptrace.New(context.Background(), client)
	if err != nil {
		return nil, err
	}

	// In a production application, use sdktrace.ProbabilitySampler with a desired probability.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(0.1)),
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("APIGwService"))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp, err
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
	tp, err := initTracer()
	if err != nil {
		log.Fatal().Msgf("failed to initialize tracing: %v", err)
	}
	defer tp.Shutdown(context.Background())

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
				otelgrpc.WithTracerProvider(tp),
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