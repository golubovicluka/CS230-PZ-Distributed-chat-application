## Installation & Setup

### Prerequisites

- **Go**: Version 1.24.5 or higher
- **Node.js**: Version 18 or higher
- **npm**: Latest version
- **Redis**: For message broadcasting (Docker recommended)

### 1. Clone the Repository

```bash
git clone git@github.com:golubovicluka/CS230-PZ-Distributed-chat-application.git
cd CS230-PZ-Distributed-chat-application
```

### 2. Start Redis with Docker

Start a Redis container for message broadcasting between chat server instances:

```bash
docker run -d --name redis-chat -p 6379:6379 redis:latest
```

### 3. Backend Setup

Install dependencies from the repository's single Go module:

```bash
go mod download
```

### 4. Frontend Setup

Navigate to the chat-app directory and install dependencies:

```bash
cd chat-app
npm install
```

## Running the Application

### Start the Load Balancer

```bash
go run ./cmd/loadbalancer
```

The load balancer listens on port 9000 by default.

### Start Chat Server(s)

In a new terminal, start at least one chat server:

```bash
go run ./cmd/chatserver
```

You can start multiple chat servers on different ports for load balancing:

```bash
# Terminal 2
go run ./cmd/chatserver \
  -listen 127.0.0.1:8081 \
  -advertise ws://127.0.0.1:8081

# Terminal 3
go run ./cmd/chatserver \
  -listen 127.0.0.1:8082 \
  -advertise ws://127.0.0.1:8082
```

### Start the Frontend

In a new terminal:

```bash
cd chat-app
npm run dev
```

The React application will start on http://localhost:5173

## Development Commands

### Frontend

```bash
npm run dev      # Start development server with hot reload
npm run build    # Build for production
npm run lint     # Run ESLint linter
npm run preview  # Preview production build
```

### Backend

```bash
go fmt ./...          # Format all Go packages
go vet ./...          # Run the standard static checks
go test ./...         # Run every backend test
go test -race ./...   # Run every test with the race detector
go build ./...        # Compile both commands and all packages
go mod tidy -diff     # Verify that module metadata is tidy
```

No external test framework, mock generator, or linter is required.

## Backend Design (Refactoring)

The backend keeps the design explicit and shallow:

- `cmd/chatserver` and `cmd/loadbalancer` are composition roots. They parse configuration, create concrete dependencies, and own startup and graceful shutdown.
- `internal/chat` owns chat sessions. One hub goroutine owns client membership; registration, removal, Redis broadcasts, and load snapshots move through channels.
- `internal/sqlite` returns a concrete message store and keeps all SQL, row mapping, ordering, and database errors in one place.
- `internal/registry` returns a concrete, bounded HTTP client for chat-server registration and load reports.
- `internal/loadbalancer` uses a small `sync.RWMutex` for its short in-memory registry critical section, where a mutex is clearer than an actor process.
- Consumer packages accept narrow interfaces only where production and test adapters both exist. Constructors return concrete structs.

The necessary runtime adapters remain Gorilla WebSocket, the SQLite driver, and the Redis client; HTTP routing, lifecycle management, concurrency, logging, configuration, and tests use Go's standard library.
