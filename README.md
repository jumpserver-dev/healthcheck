# Docker healthchecks in distroless

This is a simple example of how to use healthchecks in a Dockerfile using distroless images.

```bash
./check --help

Usage:
   check [url]
   check curl <url>
   check wget [-O output] <url>

   Example:
   check tcp://example.com:2222
   check http://example.com:8080
   check https://example.com:8443
   check curl https://example.com/health
   check wget -O check.deb https://example.com/check.deb
```

Release assets now include both:

- `check-<version>-linux-<arch>.tar.gz`
- `check_linux_<arch>.deb`

Install with `dpkg`:

```bash
wget https://github.com/jumpserver-dev/healthcheck/releases/latest/download/check_linux_amd64.deb
sudo dpkg -i check_linux_amd64.deb
```

cat Dockerfile
```yaml
# Start by building the application.
FROM golang:1.26 as build

WORKDIR /go/src/app
COPY . .

RUN go mod download
RUN CGO_ENABLED=0 go build -o /go/bin/app

# Now copy it into our base image.
FROM gcr.io/distroless/static-debian11
COPY --from=build /go/bin/app /

# download the healthcheck binary
ADD check /usr/bin/check
CMD ["/app"]
```

cat docker-compose.yml
```yaml
version: "3.9"
services:
  app:
    build: .
    image: examples/app
    ports:
      - "8080:8080"
    healthcheck:
      test: ["CMD", "check", "http://localhost:8080/health"]
      interval: 3s
      timeout: 3s
      retries: 10
```
