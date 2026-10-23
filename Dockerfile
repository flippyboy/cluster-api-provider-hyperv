FROM golang:1.27.3 AS build
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o manager .

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /workspace/manager .
USER 65532:65532
LABEL org.opencontainers.image.source=https://github.com/flippyboy/cluster-api-provider-hyperv
LABEL org.opencontainers.image.licenses=Apache-2.0
ENTRYPOINT ["/manager"]
