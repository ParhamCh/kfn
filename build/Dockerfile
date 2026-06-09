# Reference image for any kfn function. The same Dockerfile builds any function: the
# author's main.go plus the kfn runtime library compile into one static binary.
#
# FUNC_PKG selects the package to compile. From a function's own repo it is "." (the
# default). For this repo's bundled example, build with --build-arg FUNC_PKG=./examples/hello.
#
#   docker build -t harbor.lan/kfn/hello:0.1.0 \
#       --build-arg FUNC_PKG=./examples/hello -f build/Dockerfile .
#
# Or via the CLI: kfn build -f examples/hello/function.yaml --func ./examples/hello

# --- build stage -------------------------------------------------------------
FROM golang:1.26 AS build

ARG FUNC_PKG=.

WORKDIR /src

# Resolve dependencies first so they cache independently of source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static, stripped binary: no CGO so it runs on distroless/scratch; trimpath drops
# local filesystem paths for reproducibility.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags "-s -w" -o /function "${FUNC_PKG}"

# --- runtime stage -----------------------------------------------------------
# distroless/static:nonroot has no shell or package manager and runs as uid 65532,
# matching the Deployment's runAsUser/readOnlyRootFilesystem. It bundles CA certs and
# tzdata for functions that need them.
FROM gcr.io/distroless/static:nonroot

COPY --from=build /function /function

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/function"]
