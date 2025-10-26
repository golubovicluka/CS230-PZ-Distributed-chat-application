## Installation & Setup

### Prerequisites

- **Go**: Version 1.24.5 or higher
- **Node.js**: Version 18 or higher
- **npm**: Latest version
- **Redis**: For message broadcasting (Docker recommended)

### 1. Clone the Repository

```bash
git clone git@github.com:golubovicluka/Distributed-chat-application.git
```

### 2. Start Redis with Docker

Start a Redis container for message broadcasting between chat server instances:

```bash
docker run -d --name redis-chat -p 6379:6379 redis:latest
```

### 3. Backend Setup

#### Install Go Dependencies

Navigate to the server directory and install dependencies:

```bash
cd server
go mod tidy
```

### 4. Frontend Setup

Navigate to the chat-app directory and install dependencies:

```bash
cd chat-app
npm install
```

## Running the Application

Start the Load Balancer

```bash
cd loadbalancer
go run main.go
```

The load balancer will start on port 9000:

### Start Chat Server(s)

In a new terminal, start at least one chat server:

```bash
cd server
go run main.go -host 127.0.0.1 -port 8080
```

You can start multiple chat servers on different ports for load balancing:

```bash
# Terminal 2
go run main.go -host 127.0.0.1 -port 8081

# Terminal 3
go run main.go -host 127.0.0.1 -port 8082
```

### Start the Frontend

In a new terminal:

```bash
cd chat-app
npm run dev
```

The React application will start on http://localhost:5173

## Development Commands

```bash
npm run dev      # Start development server with hot reload
npm run build    # Build for production
npm run lint     # Run ESLint linter
npm run preview  # Preview production build
```

### Backend (server/ and loadbalancer/)

```bash
go run main.go   # Run the service directly
go build         # Compile the binary
go mod tidy      # Clean up dependencies (server only)
```
