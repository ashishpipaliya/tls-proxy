# TLS Bypass Proxy Service

A high-performance HTTP proxy and API bridge designed to bypass TLS fingerprinting by mimicking modern browser signatures.

## Features

- **TLS Fingerprinting Bypass**: Uses `tls-client` to mimic browser JA3 and HTTP/2 fingerprints.
- **Latest Browser Support**: Includes profiles for Chrome 133+, Firefox 135+, and Safari.
- **Dynamic Profiles**: Exposes all 60+ available profiles via an endpoint, no hardcoded switch-cases.
- **JSON API**: Full control over headers, methods, and profiles.
- **Endpoint List**:
  - `POST /request`: Perform a TLS-mimicked request.
  - `GET /profiles`: List all available browser profiles.
  - `GET /health`: Service health check.

## Supported Profiles

- **Chrome**: `chrome_120`, `chrome_124`, `chrome_131`, `chrome_133` (Default), `chrome_146`
- **Firefox**: `firefox_117`, `firefox_120`, `firefox_133`, `firefox_135`, `firefox_147`
- **Safari**: `safari_15`, `safari_16`, `safari_ios_17`, `safari_ios_18`

## Usage

### JSON API

Send a POST request to `/request` with the target details.

```bash
curl -X POST http://localhost:8080/request \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://tls.browserleaks.com/json",
    "method": "GET",
    "profile": "chrome_133",
    "headers": {
      "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"
    }
  }'
```


### 3. Running with Docker

```bash
docker build -t tls-bypass-proxy .
docker run -p 8080:8080 tls-bypass-proxy
```

## Deployment

### Container Platforms
This service is stateless and can be deployed to any container platform (Docker, Kubernetes, Fly.io, etc.).

```bash
docker build -t tls-bypass-proxy .
docker run -p 8080:8080 tls-bypass-proxy
```

### AWS Lambda
The service is pre-configured to run as an AWS Lambda function. 

> [!TIP]
> **Automatic Builds**: I have configured a GitHub Action that automatically builds the `.zip` files for both x86 and ARM64 every time you push to `main`. You can download them directly from the **Actions** tab on your GitHub repository.

1. **Build for Lambda (Manual)**:
   ```bash
   GOOS=linux GOARCH=amd64 go build -o bootstrap main.go
   zip function.zip bootstrap
   ```
2. **Deploy**: Upload `function.zip` to AWS Lambda.
3. **Trigger**: Enable **Function URL** or **API Gateway (HTTP API)**.
4. **Environment**: Ensure `AWS_LAMBDA_FUNCTION_NAME` is set (AWS sets this automatically).

## Configuration
The service can be configured via environment variables:

- `PORT`: Port to listen on for local/container deployment (Default: `8080`)
