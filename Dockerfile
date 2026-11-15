FROM golang:1.27.4 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /transit ./cmd/transit
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /transit /transit
ENTRYPOINT ["/transit"]
