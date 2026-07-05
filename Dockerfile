# syntax=docker/dockerfile:1

# ---- stage 1: build the SvelteKit UI ----
FROM node:20-alpine AS ui
WORKDIR /ui
COPY web/package.json web/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY web/ ./
RUN npm run build          # -> /ui/build (static SPA)

# ---- stage 2: build the Go binary with the UI embedded ----
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY . .
# resolve deps (generates go.sum if it isn't committed yet)
RUN go mod tidy
# drop the stub UI and embed the real build output
RUN rm -rf internal/webui/dist && mkdir -p internal/webui/dist
COPY --from=ui /ui/build/ internal/webui/dist/
# static, stripped binary
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" \
    -o /out/beholdr ./cmd/beholdr

# ---- stage 3: minimal runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/beholdr /beholdr
EXPOSE 8000
USER nonroot:nonroot
ENTRYPOINT ["/beholdr"]
