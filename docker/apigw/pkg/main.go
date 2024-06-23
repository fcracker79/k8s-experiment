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

	"github.com/go-chi/chi/v5"
	"github.com/nats-io/nats.go"
	"github.com/riandyrn/otelchi"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
)

var tracer trace.Tracer

const (
	natsCreateUserSubject = "NATS_CREATE_USER_SUBJECT"
	natsUsersSubject      = "NATS_USERS_SUBJECTS"
	natsUsersStream       = "NATS_USERS_STREAM"
	natsUrl               = "NATS_URL"
)

type Company struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
	tracer = otel.Tracer("apigw")
	// Shutdown will flush any remaining spans and shut down the exporter.
	return tracerProvider.Shutdown, nil
}

func LogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		
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
		ctx := log.WithContext(r.Context())
		log.Info().Msg("Request received")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func main() {
	otelCon, err := initConn()
	if err != nil {
		log.Fatal().Err(err).Msg("could not initialize tracer")
	}
	shutdownTracerProvider, err := initTracerProvider(otelCon)
	if err != nil {
		log.Fatal().Err(err).Msg("could not initialize tracer")
	}
	defer func() {
		if err := shutdownTracerProvider(context.Background()); err != nil {
			log.Fatal().Err(err).Msg("Failed to shutdown TracerProvider")
		}
	}()

	err = initNATS()
	if err != nil {
		log.Fatal().Err(err).Msg("could not initialize NATS")
	}
	startHTTPServer()
}

func startHTTPServer() {
	r := chi.NewRouter()

	r.Use(otelchi.Middleware("apigw", otelchi.WithChiRoutes(r)))
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

	// Async endpoints
	r.Post("/async/users", asyncCreateUser)

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
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	connection, err := createGrpcConnection()
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("did not connect")
	}
	defer connection.Close()
	c := pb.NewUserServiceClient(connection)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	user, err := c.GetUser(ctx, &pb.User{Id: id})
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not fetch user")
	}
	data, _ := json.Marshal(user)
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func createUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Fatal().Err(err).Msg("could not read request body")
	}
	var user pb.User
	err = json.Unmarshal(body, &user)
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not unmarshal request body")
	}
	connection, err := createGrpcConnection()
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("did not connect")
	}
	defer connection.Close()
	c := pb.NewUserServiceClient(connection)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	createdUser, err := c.CreateUser(ctx, &user)
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not create user")
	}
	data, _ := json.Marshal(createdUser)
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func asyncCreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := zerolog.Ctx(ctx)

	logger.Info().Msgf("Received async create user request, ctx %v+", ctx)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Fatal().Err(err).Msg("could not read request body")
	}

	conn, err := createNATSConnection()
	if err != nil {
		logger.Fatal().Err(err).Msg("could not connect to NATS")
	}

	subject, err := getCreateUserNATSSubject()
	if err != nil {
		logger.Fatal().Err(err).Msg("could not get NATS subject")
	}
	header := make(nats.Header)

	_, span := tracer.Start(ctx, "PublishWithTrace")
	defer span.End()

	headerCarrier := propagation.HeaderCarrier(header)
	otel.GetTextMapPropagator().Inject(ctx, headerCarrier)
	logger.Info().Msgf("Publishing message to subject %s, headers %v, headerCarrier %v", subject, header, headerCarrier)
	msg := &nats.Msg{
		Subject: subject,
		Data:    body,
		Header:  header,
	}

	if conn.PublishMsg(msg); err != nil {
		logger.Fatal().Err(err).Msg("could not publish message")
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
}

func deleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	connection, err := createGrpcConnection()
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("did not connect")
	}
	defer connection.Close()
	c := pb.NewUserServiceClient(connection)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, err = c.DeleteUser(ctx, &pb.User{Id: id})
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not delete user")
	}
	w.Write([]byte("User deleted"))
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

func getHTTPClient() *http.Client {
	return &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
}

func getCompany(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	req, err := newHTTPRequest(ctx, "GET", fmt.Sprintf("%s/companies/%s", getCompanyHttpEndpoint(), id), nil)
	req = req.WithContext(ctx)
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not create fetch company")
	}
	resp, err := getHTTPClient().Do(req)
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not fetch company")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not read resp body")
		}
		bodyString := string(bodyBytes)
		w.Write([]byte(bodyString))
	} else {
		w.Write([]byte("Company not found"))
	}
}

func newHTTPRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}

	// Very important: without this, it's impossible to propagate the trace
	return req.WithContext(ctx), nil
}

func getAllCompanies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req, err := newHTTPRequest(ctx, "GET", fmt.Sprintf("%s/companies", getCompanyHttpEndpoint()), nil)
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not create fetch companies")
	}
	req = req.WithContext(ctx)
	resp, err := getHTTPClient().Do(req)
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not fetch companies")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not read resp body")
		}
		bodyString := string(bodyBytes)
		w.Write([]byte(bodyString))
	} else {
		w.Write([]byte("Could not fetch companies"))
	}
}

func createCompany(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not read request body")
	}

	var company Company
	err = json.Unmarshal(body, &company)
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not unmarshal request body")
	}

	jsonStr := string(body)

	req, err := newHTTPRequest(ctx, "POST", fmt.Sprintf("%s/companies", getCompanyHttpEndpoint()), strings.NewReader(jsonStr))
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not create company request")
	}
	req = req.WithContext(ctx)
	resp, err := getHTTPClient().Do(req)
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not create company")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not read resp body")
		}
		bodyString := string(bodyBytes)
		w.Write([]byte(bodyString))
	} else {
		w.Write([]byte("Could not create company"))
	}
}

func deleteCompany(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	req, err := newHTTPRequest(ctx, "DELETE", fmt.Sprintf("%s/companies/%s", getCompanyHttpEndpoint(), id), nil)
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not create delete request")
	}
	req = req.WithContext(ctx)
	resp, err := getHTTPClient().Do(req)
	if err != nil {
		zerolog.Ctx(ctx).Fatal().Err(err).Msg("could not delete company")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		w.Write([]byte("Company deleted"))
	} else {
		w.Write([]byte("Could not delete company"))
	}
}

func createNATSConnection() (*nats.Conn, error) {
	return nats.Connect(getEnvString(natsUrl))
}

func getEnvStringOrError(env string) (string, error) {
	if envVar, exists := os.LookupEnv(env); exists {
		return envVar, nil
	} else {
		return "", fmt.Errorf("%s not set", env)
	}
}

func getNATSStream() (string, error) {
	return getEnvStringOrError(natsUsersStream)
}

func getNATSUsersSubjects() ([]string, error) {
	strNatsSubjects, err := getEnvStringOrError(natsUsersSubject)
	if err != nil {
		return nil, err
	}
	return strings.Split(strNatsSubjects, ","), nil
}

func getNATSSubjects() ([]string, error) {
	var res []string
	usersSubjects, err := getNATSUsersSubjects()
	if err != nil {
		return nil, err
	}
	res = append(res, usersSubjects...)
	return res, nil
}

func getCreateUserNATSSubject() (string, error) {
	return getEnvStringOrError(natsCreateUserSubject)
}

func initNATS() error {
	conn, err := createNATSConnection()
	if err != nil {
		return fmt.Errorf("could not create NATS connection: %w", err)
	}
	js, _ := conn.JetStream()

	streamName, err := getNATSStream()
	if err != nil {
		return fmt.Errorf("could not get NATS stream: %w", err)
	}

	natsSubjects, err := getNATSSubjects()
	if err != nil {
		return fmt.Errorf("could not get NATS subjects: %w", err)
	}

	_, err = js.AddStream(&nats.StreamConfig{
		Name:     streamName,
		Subjects: natsSubjects,
	})
	if err != nil {
		return fmt.Errorf("could not create NATS stream %s(%v): %w", streamName, natsSubjects, err)
	}
	return nil
}
