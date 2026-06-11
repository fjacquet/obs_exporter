# syntax=docker/dockerfile:1
# Build context is the repo root: docker build -f deploy/mockecs/Dockerfile .
FROM golang:1.26.4 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/mockecs ./cmd/mockecs

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/mockecs /mockecs
USER nonroot:nonroot
EXPOSE 4443
ENTRYPOINT ["/mockecs"]
