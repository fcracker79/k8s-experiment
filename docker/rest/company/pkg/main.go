package main

import (
    "database/sql"
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "time"
	"context"

    _ "modernc.org/sqlite"
    "github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog"

	"github.com/riandyrn/otelchi"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
)

type Company struct {
    ID          string    `json:"id"`
    Name        string    `json:"name"`
    Description string    `json:"description"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}

var db *sql.DB

func getEnvString(env string) string {
    if envVar, exists := os.LookupEnv(env); exists {
        return envVar
    } else {
        panic(fmt.Sprintf("%s not set", env))
    }
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
			semconv.ServiceNameKey.String("CompanyService"),
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

func main() {
    db = InitDB("./storage.db")
    CreateTable(db)
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

    r := chi.NewRouter()
	r.Use(otelchi.Middleware("company", otelchi.WithChiRoutes(r)))
	r.Use(LogMiddleware)
    r.Route("/companies", func(r chi.Router) {
        r.Get("/", listCompanies)    // GET List Companies
        r.Post("/", createCompany)   // POST Create a new Company

        r.Route("/{id}", func(r chi.Router) {
            r.Get("/", getCompany)    // GET a specific Company
            r.Put("/", updateCompany) // PUT Update a specific Company
            r.Delete("/", deleteCompany) // DELETE a specific Company
        })
    })

    http.ListenAndServe(fmt.Sprintf(":%s", getEnvString("TCP_PORT")), r)
}

func InitDB(filepath string) *sql.DB {
    var err error
    db, err = sql.Open("sqlite", filepath)
    if err != nil {
        panic(err)
    }
    if db == nil {
        panic("db nil")
    }
    return db
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

func CreateTable(db *sql.DB) {
    sql_table := `
    CREATE TABLE IF NOT EXISTS Company(
        ID TEXT NOT NULL PRIMARY KEY,
        Name TEXT,
        Description TEXT,
        CreatedAt TIMESTAMP,
        UpdatedAt TIMESTAMP);
    `

    _, err := db.Exec(sql_table)
    if err != nil {
        panic(err)
    }
}

func listCompanies(w http.ResponseWriter, r *http.Request) {
    rows, err := db.Query("SELECT * FROM Company")
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    defer rows.Close()

    var result []Company
    for rows.Next() {
        company := Company{}
        err := rows.Scan(&company.ID, &company.Name, &company.Description, &company.CreatedAt, &company.UpdatedAt)
        if err != nil {
            http.Error(w, err.Error(), 500)
            return
        }
        result = append(result, company)
    }

    json.NewEncoder(w).Encode(result)
}

func createCompany(w http.ResponseWriter, r *http.Request) {
    var company Company
    json.NewDecoder(r.Body).Decode(&company)

    _, err := db.Exec("INSERT INTO Company (ID, Name, Description, CreatedAt, UpdatedAt) values (?, ?, ?, ?, ?)", company.ID, company.Name, company.Description, time.Now(), time.Now())
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }

    json.NewEncoder(w).Encode(company)
}

func getCompany(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")

    row := db.QueryRow("SELECT * FROM Company WHERE ID = ?", id)

    var company Company
    err := row.Scan(&company.ID, &company.Name, &company.Description, &company.CreatedAt, &company.UpdatedAt)
    if err != nil {
        if err == sql.ErrNoRows {
            http.Error(w, "No such company", 404)
            return
        }
        http.Error(w, err.Error(), 500)
        return
    }

    json.NewEncoder(w).Encode(company)
}

func updateCompany(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")

    var company Company
    json.NewDecoder(r.Body).Decode(&company)

    _, err := db.Exec("UPDATE Company SET Name = ?, Description = ?, UpdatedAt = ? WHERE ID = ?", company.Name, company.Description, time.Now(), id)
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }

    json.NewEncoder(w).Encode(company)
}

func deleteCompany(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")

    _, err := db.Exec("DELETE FROM Company WHERE ID = ?", id)
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }

    json.NewEncoder(w).Encode(fmt.Sprintf("Company with ID %s deleted successfully", id))
}
