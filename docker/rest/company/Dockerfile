FROM golang:1.22.3 AS builder

WORKDIR /app
ADD . /app

ENV CGO_ENABLED=0
RUN go build pkg/main.go

#FROM scratch
FROM alpine:latest

COPY --from=builder /app/main /bin/main

CMD ["/bin/main"]

