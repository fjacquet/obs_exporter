# syntax=docker/dockerfile:1
FROM golang:1.26.4 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/obs_exporter .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/obs_exporter /obs_exporter
USER nonroot:nonroot
EXPOSE 9438
ENTRYPOINT ["/obs_exporter"]
CMD ["--config", "/etc/obs_exporter/config.yaml"]
