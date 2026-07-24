FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /cinema ./cmd \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /cinema /app/cinema
COPY --from=build /migrate /app/migrate
COPY --chown=nonroot:nonroot static /app/static
COPY --chown=nonroot:nonroot migrations /app/migrations
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/cinema"]
