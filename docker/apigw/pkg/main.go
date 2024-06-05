package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	pb "github.com/fcracker79/k8s-experiment/docker/rest/apigw/proto/user"

	"contrib.go.opencensus.io/exporter/ocagent"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opencensus.io/plugin/ocgrpc"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/plugin/ochttp/propagation/b3"
	"go.opencensus.io/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Company struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func LogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.FromContext(r.Context())
		traceID := span.SpanContext().TraceID.String()
		spanID := span.SpanContext().SpanID.String()
		log := zerolog.New(os.Stderr).With().Timestamp().
			Str("traceId", traceID).
			Str("spanId", spanID).
			Logger()
		ctx := log.WithContext(r.Context())
		log.Info().Msg("Request received")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func main() {
	r := chi.NewRouter()
	ocagentHost := os.Getenv("OC_AGENT_HOST")
	oce, err := ocagent.NewExporter(
		ocagent.WithInsecure(),
		ocagent.WithReconnectionPeriod(5*time.Second),
		ocagent.WithAddress(ocagentHost),
		ocagent.WithServiceName("apigw"))
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create ocagent-exporter")
	}
	trace.RegisterExporter(oce)

	r.Use(func(next http.Handler) http.Handler {
		return &ochttp.Handler{
			Handler:     next,
			Propagation: &b3.HTTPFormat{},
		}
	})
	r.Use(LogMiddleware)

	// User endpoints
	r.Get("/users/{id}", getUser)
	r.Post("/users", createUser)
	r.Delete("/users/{id}", deleteUser)

	// Company endpoints
	r.Get("/companies/{id}", getCompany)
	r.Get("/companies", getAllCompanies)
	r.Post("/companies", createCompany)
	r.Delete("/companies/{id}", deleteCompany)

	tcpPort := getEnvString("TCP_PORT")
	fmt.Printf("Listening port %s\n", tcpPort)
	http.ListenAndServe(fmt.Sprintf(":%s", tcpPort), r)
}

func getUserGrpcEndpoint() string {
	return fmt.Sprintf("%s:%s", getEnvString("GRPC_USER_HOST"), getEnvString("GRPC_USER_PORT"))
}

func getCompanyHttpEndpoint() string {
	return fmt.Sprintf("http://%s:%s", getEnvString("REST_COMPANY_HOST"), getEnvString("REST_COMPANY_PORT"))
}

func getEnvString(env string) string {
	if envVar, exists := os.LookupEnv(env); exists {
		return envVar
	} else {
		panic(fmt.Sprintf("%s not set", env))
	}
}

func getUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	connection, err := createGrpcConnection()
	if err != nil {
		zerolog.Ctx(r.Context()).Fatal().Err(err).Msg("did not connect")
	}
	defer connection.Close()
	c := pb.NewUserServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	user, err := c.GetUser(ctx, &pb.User{Id: id})
	if err != nil {
		log.Fatal().Err(err).Msg("could not fetch user")
	}
	data, _ := json.Marshal(user)
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func createUser(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Fatal().Err(err).Msg("could not read request body")
	}
	var user pb.User
	err = json.Unmarshal(body, &user)
	if err != nil {
		zerolog.Ctx(r.Context()).Fatal().Err(err).Msg("could not unmarshal request body")
	}
	connection, err := createGrpcConnection()
	if err != nil {
		zerolog.Ctx(r.Context()).Fatal().Err(err).Msg("did not connect")
	}
	defer connection.Close()
	c := pb.NewUserServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	createdUser, err := c.CreateUser(ctx, &user)
	if err != nil {
		zerolog.Ctx(r.Context()).Fatal().Err(err).Msg("could not create user")
	}
	data, _ := json.Marshal(createdUser)
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func deleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	connection, err := createGrpcConnection()
	if err != nil {
		zerolog.Ctx(r.Context()).Fatal().Err(err).Msg("did not connect")
	}
	defer connection.Close()
	c := pb.NewUserServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = c.DeleteUser(ctx, &pb.User{Id: id})
	if err != nil {
		zerolog.Ctx(r.Context()).Fatal().Err(err).Msg("could not delete user")
	}
	w.Write([]byte("User deleted"))
}

func createGrpcConnection() (*grpc.ClientConn, error) {
	connection, err := grpc.NewClient(
		getUserGrpcEndpoint(), 
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(new(ocgrpc.ClientHandler)),
	)
	return connection, err
}

func getHTTPClient() *http.Client {
	return &http.Client{
		Transport: &ochttp.Transport{},
	}
}

func getCompany(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	resp, err := getHTTPClient().Get(fmt.Sprintf("%s/companies/%s", getCompanyHttpEndpoint(), id))
	if err != nil {
		zerolog.Ctx(r.Context()).Fatal().Err(err).Msg("could not fetch company")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			zerolog.Ctx(r.Context()).Fatal().Err(err).Msg("could not read resp body")
		}
		bodyString := string(bodyBytes)
		w.Write([]byte(bodyString))
	} else {
		w.Write([]byte("Company not found"))
	}
}

func getAllCompanies(w http.ResponseWriter, r *http.Request) {
	resp, err := getHTTPClient().Get(fmt.Sprintf("%s/companies", getCompanyHttpEndpoint()))
	if err != nil {
		zerolog.Ctx(r.Context()).Fatal().Err(err).Msg("could not fetch companies")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			zerolog.Ctx(r.Context()).Fatal().Err(err).Msg("could not read resp body")
		}
		bodyString := string(bodyBytes)
		w.Write([]byte(bodyString))
	} else {
		w.Write([]byte("Could not fetch companies"))
	}
}

func createCompany(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		zerolog.Ctx(r.Context()).Fatal().Err(err).Msg("could not read request body")
	}

	var company Company
	err = json.Unmarshal(body, &company)
	if err != nil {
		zerolog.Ctx(r.Context()).Fatal().Err(err).Msg("could not unmarshal request body")
	}

	jsonStr := string(body)

	resp, err := getHTTPClient().Post(fmt.Sprintf("%s/companies", getCompanyHttpEndpoint()), "application/json", strings.NewReader(jsonStr))
	if err != nil {
		zerolog.Ctx(r.Context()).Fatal().Err(err).Msg("could not create company")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Fatal().Err(err).Msg("could not read resp body")
		}
		bodyString := string(bodyBytes)
		w.Write([]byte(bodyString))
	} else {
		w.Write([]byte("Could not create company"))
	}
}

func deleteCompany(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	req, err := http.NewRequest("DELETE", fmt.Sprintf("%s/companies/%s", getCompanyHttpEndpoint(), id), nil)
	if err != nil {
		zerolog.Ctx(r.Context()).Fatal().Err(err).Msg("could not create delete request")
	}

	resp, err := getHTTPClient().Do(req)
	if err != nil {
		zerolog.Ctx(r.Context()).Fatal().Err(err).Msg("could not delete company")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		w.Write([]byte("Company deleted"))
	} else {
		w.Write([]byte("Could not delete company"))
	}
}
