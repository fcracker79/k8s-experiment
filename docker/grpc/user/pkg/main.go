package main

import (
	"context"
	"database/sql"
    "fmt"
	"log"
	"net"
    "os"
	"time"

	_ "modernc.org/sqlite"
	"google.golang.org/grpc"
	pb "github.com/fcracker79/k8s-experiment/docker/grpc/user/proto"
	"go.opencensus.io/trace"
	"contrib.go.opencensus.io/exporter/ocagent"
	"go.opencensus.io/plugin/ocgrpc"
	"github.com/rs/zerolog"
)
 
type server struct {
	pb.UnimplementedUserServiceServer
	db *sql.DB
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
	db, err := sql.Open("sqlite", "./user.db")
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
    CreateTable(db)
	ocagentHost := os.Getenv("OC_AGENT_HOST")
	oce, err := ocagent.NewExporter(
		ocagent.WithInsecure(),
		ocagent.WithReconnectionPeriod(5 * time.Second),
		ocagent.WithAddress(ocagentHost),
		ocagent.WithServiceName("voting"))
	if err != nil {
		log.Fatalf("Failed to create ocagent-exporter: %v", err)
	}
	trace.RegisterExporter(oce)

    tcpPort := getEnvString("TCP_PORT")
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", tcpPort))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer(
		grpc.StatsHandler(&ocgrpc.ServerHandler{}),
		grpc.UnaryInterceptor(serverLoggingInterceptor))
	pb.RegisterUserServiceServer(s, &server{db: db})

	log.Printf("Server listening on port %s", tcpPort)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func serverLoggingInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	span := trace.FromContext(ctx)
	traceID := span.SpanContext().TraceID.String()
	spanID := span.SpanContext().SpanID.String()
	log := zerolog.New(os.Stderr).With().Timestamp().
		Str("traceId", traceID).
		Str("spanId", spanID).
		Logger()
	log.Info().Msg("Request received")
	return handler(log.WithContext(ctx), req)
}