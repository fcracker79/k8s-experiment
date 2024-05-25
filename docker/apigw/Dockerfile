FROM golang:1.22.3 AS builder

WORKDIR /app
ADD go.mod /app
ADD go.sum /app
RUN CGO_ENABLED=0 GOPRIVATE=github.com/fcracker79/k8s-experiment go mod download

ADD . /app

RUN CGO_ENABLED=0 GOPRIVATE=github.com/fcracker79/k8s-experiment go build pkg/main.go

#FROM scratch
FROM alpine:latest

COPY --from=builder /app/main /bin/main

CMD ["/bin/main"]

