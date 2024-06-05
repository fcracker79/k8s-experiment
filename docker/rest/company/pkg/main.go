package main

import (
    "database/sql"
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "time"

    _ "modernc.org/sqlite"
    "github.com/go-chi/chi/v5"
	"contrib.go.opencensus.io/exporter/ocagent"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog"
	"go.opencensus.io/trace"
	"go.opencensus.io/plugin/ochttp/propagation/b3"
	"go.opencensus.io/plugin/ochttp"
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

func main() {
    db = InitDB("./storage.db")
    CreateTable(db)
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
    r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return &ochttp.Handler{
			Handler:     next,
			Propagation: &b3.HTTPFormat{},
		}
	})
    r.Route("/companies", func(r chi.Router) {
        r.Get("/", listCompanies)    // GET List Companies
        r.Post("/", createCompany)   // POST Create a new Company

        r.Route("/{id}", func(r chi.Router) {
            r.Get("/", getCompany)    // GET a specific Company
            r.Put("/", updateCompany) // PUT Update a specific Company
            r.Delete("/", deleteCompany) // DELETE a specific Company
        })
    })
	r.Use(LogMiddleware)

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
