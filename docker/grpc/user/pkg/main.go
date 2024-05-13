package main

import (
	"context"
	"database/sql"
	"log"
	"net"
	"time"

	_ "modernc.org/sqlite"
	"google.golang.org/grpc"
	pb "github.com/fcracker79/k8s-experiment/docker/grpc/user/proto"
)

type server struct {
	pb.UnimplementedUserServiceServer
	db *sql.DB
}

func (s *server) CreateUser(ctx context.Context, in *pb.User) (*pb.User, error) {
	stmt, err := s.db.Prepare("INSERT INTO users(name, description, created_at, updated_at) VALUES(?,?,?,?)")
	if err != nil {
		return nil, err
	}
	_, err = stmt.Exec(in.Name, in.Description, in.CreatedAt, in.UpdatedAt)
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

func main() {
	db, err := sql.Open("sqlite", "./user.db")
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	lis, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterUserServiceServer(s, &server{db: db})

	log.Printf("Server listening on port 8080")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
