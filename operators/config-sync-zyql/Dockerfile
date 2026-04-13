# Build stage
FROM reg.zyql.com/library/golang:1.25.4-alpine AS builder

WORKDIR /workspace

# 配置 Go 代理
ENV GOPROXY=https://goproxy.cn,https://goproxy.io,direct
ENV GOSUMDB=off

# Copy go mod files
COPY go.mod go.mod
COPY go.sum go.sum

# Download dependencies
RUN go mod download

# Copy source code
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o manager main.go

# Runtime stage
FROM reg.zyql.com/library/alpine:3.18

WORKDIR /

# Copy binary from builder
COPY --from=builder /workspace/manager .

# Use non-root user
USER 65532:65532

ENTRYPOINT ["/manager"]
