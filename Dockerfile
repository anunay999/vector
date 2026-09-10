# vector container image — for headless and CI use.
#
# The gateway binds loopback by default. Inside a container, set the listen
# addresses to 0.0.0.0 in config.yaml, for example:
#
#   listen:
#     anthropic: 0.0.0.0:7331
#     openai: 0.0.0.0:7331
#     admin: 127.0.0.1:7333
#
# The /admin/status endpoint is restricted to loopback even when the data plane
# is exposed, so it stays private.
#
# Build:  docker build -t vector .
# Run:    docker run --rm -p 7331:7331 \
#           -v "$HOME/.config/vector:/root/.config/vector:ro" \
#           -e OPENROUTER_API_KEY=sk-or-... vector

FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/vector ./cmd/vector

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/vector /usr/local/bin/vector
EXPOSE 7331
ENTRYPOINT ["/usr/local/bin/vector"]
CMD ["serve"]
