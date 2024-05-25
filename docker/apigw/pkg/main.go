package main

import (
    "context"
    "encoding/json"
    "fmt"
    "io/ioutil"
    "log"
    "net/http"
    "os"
    "strings"
    "time"

    pb "github.com/fcracker79/k8s-experiment/docker/rest/apigw/proto/user"

    "github.com/go-chi/chi/v5"
    "google.golang.org/grpc"
)

type Company struct {
    ID          string    `json:"id"`
    Name        string    `json:"name"`
    Description string    `json:"description"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}

func main() {
    r := chi.NewRouter()

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
    connection, err := grpc.Dial(getUserGrpcEndpoint(), grpc.WithInsecure())
    if err != nil {
        log.Fatalf("did not connect: %v", err)
    }
    defer connection.Close()
    c := pb.NewUserServiceClient(connection)
    ctx, cancel := context.WithTimeout(context.Background(), time.Second)
    defer cancel()
    user, err := c.GetUser(ctx, &pb.User{Id: id})
    if err != nil {
        log.Fatalf("could not fetch user: %v", err)
    }
    data, _ := json.Marshal(user)
    w.Header().Set("Content-Type", "application/json")
    w.Write(data)
}

func createUser(w http.ResponseWriter, r *http.Request) {
    body, err := ioutil.ReadAll(r.Body)
    if err != nil {
        log.Fatalf("could not read request body: %v", err)
    }
    var user pb.User
    err = json.Unmarshal(body, &user)
    if err != nil {
        log.Fatalf("could not unmarshal request body: %v", err)
    }
    connection, err := grpc.Dial(getUserGrpcEndpoint(), grpc.WithInsecure())
    if err != nil {
        log.Fatalf("did not connect: %v", err)
    }
    defer connection.Close()
    c := pb.NewUserServiceClient(connection)
    ctx, cancel := context.WithTimeout(context.Background(), time.Second)
    defer cancel()
    createdUser, err := c.CreateUser(ctx, &user)
    if err != nil {
        log.Fatalf("could not create user: %v", err)
    }
    data, _ := json.Marshal(createdUser)
    w.Header().Set("Content-Type", "application/json")
    w.Write(data)
}

func deleteUser(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
    connection, err := grpc.Dial(getUserGrpcEndpoint(), grpc.WithInsecure())
    if err != nil {
        log.Fatalf("did not connect: %v", err)
    }
    defer connection.Close()
    c := pb.NewUserServiceClient(connection)
    ctx, cancel := context.WithTimeout(context.Background(), time.Second)
    defer cancel()
    _, err = c.DeleteUser(ctx, &pb.User{Id: id})
    if err != nil {
        log.Fatalf("could not delete user: %v", err)
    }
    w.Write([]byte("User deleted"))
}

func getCompany(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
    resp, err := http.Get(fmt.Sprintf("%s/companies/%s", getCompanyHttpEndpoint(), id))
    if err != nil {
        log.Fatalf("could not fetch company: %v", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode == http.StatusOK {
        bodyBytes, err := ioutil.ReadAll(resp.Body)
        if err != nil {
            log.Fatal(err)
        }
        bodyString := string(bodyBytes)
        w.Write([]byte(bodyString))
    } else {
        w.Write([]byte("Company not found"))
    }
}

func getAllCompanies(w http.ResponseWriter, r *http.Request) {
    resp, err := http.Get(fmt.Sprintf("%s/companies", getCompanyHttpEndpoint()))
    if err != nil {
        log.Fatalf("could not fetch companies: %v", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode == http.StatusOK {
        bodyBytes, err := ioutil.ReadAll(resp.Body)
        if err != nil {
            log.Fatal(err)
        }
        bodyString := string(bodyBytes)
        w.Write([]byte(bodyString))
    } else {
        w.Write([]byte("Could not fetch companies"))
    }
}

func createCompany(w http.ResponseWriter, r *http.Request) {
    body, err := ioutil.ReadAll(r.Body)
    if err != nil {
        log.Fatalf("could not read request body: %v", err)
    }

    var company Company
    err = json.Unmarshal(body, &company)
    if err != nil {
        log.Fatalf("could not unmarshal request body: %v", err)
    }

    jsonStr := string(body)

    resp, err := http.Post(fmt.Sprintf("%s//companies", getCompanyHttpEndpoint()), "application/json", strings.NewReader(jsonStr))
    if err != nil {
        log.Fatalf("could not create company: %v", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode == http.StatusOK {
        bodyBytes, err := ioutil.ReadAll(resp.Body)
        if err != nil {
            log.Fatal(err)
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
        log.Fatalf("could not create delete request: %v", err)
    }

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        log.Fatalf("could not delete company: %v", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode == http.StatusOK {
        w.Write([]byte("Company deleted"))
    } else {
        w.Write([]byte("Could not delete company"))
    }
}
