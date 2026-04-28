# Backend Server

Google Classroom AI-powered catch-up lesson generator backend API.

## Prerequisites

- Go 1.25.6
- MongoDB
- Google Cloud Project with Classroom & Drive APIs enabled
- OpenAI API key

## Environment Variables

Copy `.env.example` to `.env` and fill in your credentials:

```bash
cp .env.example .env
```

Then edit `.env` with your API keys and configuration values.

## Installation

```bash
# Install dependencies
make install

# Or manually
go mod download
```

## Running

### Development Mode (with hot reload)
```bash
make dev
```

### Production Mode
```bash
make run
```

### Build Binary
```bash
make build
./bin/api
```

## API Documentation

Once running, access Swagger docs at:
```
http://localhost:8080/api/docs/index.html
```

## Available Commands

```bash
make install    # Install dependencies
make dev        # Run with hot reload (Air)
make run        # Run normally
make build      # Build binary to bin/api
make clean      # Remove build artifacts
make fmt        # Format code
make stop       # Stop server on port 8080
```

## Project Structure

```
server/
├── cmd/api/          # Application entry point
├── config/           # Configuration
├── internal/
│   ├── handlers/     # HTTP handlers
│   ├── middleware/   # Middleware (auth, CORS)
│   ├── models/       # Data models
│   ├── routes/       # Route registration
│   └── services/     # Business logic
└── docs/             # Swagger documentation
```
"refresh" 
