FROM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /watcher ./cmd/watcher

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /watcher /watcher
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/watcher"]
