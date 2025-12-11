# go-wrk Distributed Load Testing

This is a distributed load testing extension for `go-wrk` that allows you to run load tests across multiple machines to overcome single-machine bandwidth limitations and generate significantly higher load.

## Architecture

The distributed system consists of three main components:

1. **Coordinator** - Central node that distributes jobs and aggregates results
2. **Worker** - Multiple worker nodes that execute actual load tests
3. **Client** - Command-line tool to initiate distributed tests

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Client    │────▶│ Coordinator │────▶│   Worker 1  │
└─────────────┘     └─────────────┘     └─────────────┘
                           │                    │
                           │                    │
                           ▼                    ▼
                    ┌─────────────┐     ┌─────────────┐
                    │   Results   │◀────│   Worker N  │
                    │ Aggregation │     └─────────────┘
                    └─────────────┘
```

## Installation

### Build All Components

```bash
# Build coordinator
go build -o go-wrk-coordinator coordinator.go

# Build worker
go build -o go-wrk-worker worker.go

# Build distributed client
go build -o go-wrk-dist go-wrk-dist.go

# Build original go-wrk (optional, for comparison)
go build -o go-wrk go-wrk.go
```

## Usage

### 1. Start Coordinator

On the coordinator machine (typically your local machine or a central server):

```bash
./go-wrk-coordinator -port 8080
```

### 2. Start Worker Nodes

On each worker machine (can be multiple machines or multiple instances on same machine):

```bash
# On worker machine 1 (with different port)
./go-wrk-worker -port 8081 -id worker1

# On worker machine 2
./go-wrk-worker -port 8082 -id worker2

# On worker machine 3 (on same machine, different port)
./go-wrk-worker -port 8083 -id worker3
```

### 3. Run Distributed Load Test

Using the distributed client tool:

```bash
# Basic test with 2 workers
./go-wrk-dist -c 100 -d 30 \
  -workers "192.168.1.100:8081,192.168.1.101:8082" \
  http://example.com

# Advanced test with custom headers and body
./go-wrk-dist -c 50 -d 60 \
  -coordinator "http://coordinator-host:8080" \
  -workers "worker1:8081,worker2:8081,worker3:8081" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer token123" \
  -body '{"test": "payload"}' \
  -M POST \
  https://api.example.com/endpoint
```

## Command Line Options

### Coordinator Options
```
-port      HTTP port for coordinator (default: 8080)
```

### Worker Options
```
-port      HTTP port for worker (default: 8081)
-id        Worker ID (default: hostname)
```

### Distributed Client Options
All standard `go-wrk` options are supported, plus:

```
-coordinator   Coordinator URL (default: http://localhost:8080)
-workers       Comma-separated list of worker nodes (host:port)
               (default: localhost:8081)
```

Standard go-wrk options:
```
-c        Number of goroutines per worker (default: 10)
-d        Duration of test in seconds (default: 10)
-H        Header to add to each request (can be used multiple times)
-M        HTTP method (default: GET)
-T        Socket/request timeout in ms (default: 1000)
-body     Request body string or @filename
-redir    Allow Redirects (default: false)
-no-c     Disable Compression
-no-ka    Disable KeepAlive
-no-vr    Skip verifying SSL certificate
-cert     CA certificate file
-key      Private key file name
-ca       CA file to verify peer against
-http     Use HTTP/2 (default: true)
```

## API Endpoints

### Coordinator API
- `POST /start` - Start a new distributed load test
- `GET /status` - Get current test status
- `GET /results` - Get aggregated test results
- `POST /worker/report` - Worker reports results (internal use)

### Worker API
- `POST /start` - Start a load test on this worker
- `POST /stop` - Stop current load test
- `GET /status` - Get worker status
- `GET /health` - Health check endpoint

## Performance Benefits

### Breaking Bandwidth Limitations
Single machine limitations:
- Network interface card (NIC) bandwidth (1Gbps, 10Gbps, etc.)
- TCP connection limits
- CPU and memory constraints

With distributed testing:
- Aggregate bandwidth from multiple machines
- Distribute connection load across nodes
- Scale horizontally by adding more workers

### Example Scaling
Assuming each worker can generate:
- 10,000 requests/sec
- 100MB/sec bandwidth

With 10 workers:
- **100,000 requests/sec** total
- **1GB/sec** total bandwidth
- Linear scaling with worker count

## Deployment Scenarios

### 1. Local Testing (Multiple Ports)
```bash
# Terminal 1: Coordinator
./go-wrk-coordinator -port 8080

# Terminal 2: Worker 1
./go-wrk-worker -port 8081 -id worker1

# Terminal 3: Worker 2
./go-wrk-worker -port 8082 -id worker2

# Terminal 4: Run test
./go-wrk-dist -c 100 -d 30 -workers "localhost:8081,localhost:8082" http://localhost:8080
```

### 2. Cross-Machine Testing
```bash
# On coordinator machine (192.168.1.50)
./go-wrk-coordinator -port 8080

# On worker machine 1 (192.168.1.100)
./go-wrk-worker -port 8081 -id worker1

# On worker machine 2 (192.168.1.101)
./go-wrk-worker -port 8081 -id worker2

# On client machine
./go-wrk-dist -c 200 -d 60 \
  -coordinator "http://192.168.1.50:8080" \
  -workers "192.168.1.100:8081,192.168.1.101:8081" \
  http://target-service.com
```

### 3. Cloud Deployment
Deploy workers on cloud instances (AWS EC2, GCP VMs, etc.) and coordinator on a central server.

## Monitoring and Results

### Real-time Status
```bash
# Check coordinator status
curl http://localhost:8080/status

# Check worker status
curl http://worker-host:8081/status
```

### Final Results
Results include:
- Total requests across all workers
- Total errors and error distribution
- Aggregate request rate (requests/sec)
- Aggregate transfer rate (MB/sec)
- Latency percentiles (p50, p90, p99, etc.)
- Per-worker statistics

## Best Practices

1. **Worker Placement**: Place workers close to target service to minimize network latency
2. **Coordinator Location**: Coordinator can be anywhere with network access to workers
3. **Scaling**: Start with 2-3 workers, add more as needed
4. **Monitoring**: Monitor worker CPU, memory, and network usage during tests
5. **Security**: Use HTTPS for coordinator-worker communication in production

## Limitations and Future Improvements

### Current Limitations
- Simple HTTP-based communication (no encryption)
- Basic error handling
- No persistent job queue
- Manual worker deployment

### Planned Improvements
- [ ] TLS encryption for coordinator-worker communication
- [ ] Web-based dashboard for monitoring
- [ ] Automated worker discovery
- [ ] Support for different load patterns (ramp-up, spike, etc.)
- [ ] Integration with existing monitoring systems

## Troubleshooting

### Common Issues

1. **Workers not reporting results**
   - Check network connectivity between workers and coordinator
   - Verify worker can reach coordinator URL
   - Check worker logs for errors

2. **Low request rate**
   - Increase goroutines per worker (`-c` parameter)
   - Add more worker nodes
   - Check target service capacity

3. **Connection errors**
   - Adjust timeout values (`-T` parameter)
   - Check DNS resolution on workers
   - Verify target service is reachable from workers

### Debug Mode
Add verbose logging to coordinator and workers by setting environment variable:
```bash
export GO_WRK_DEBUG=1
./go-wrk-coordinator -port 8080
```

## Comparison with Original go-wrk

| Feature | go-wrk | go-wrk-distributed |
|---------|--------|-------------------|
| Single machine | ✓ | ✓ (via workers) |
| Multiple machines | ✗ | ✓ |
| Bandwidth scaling | Limited to single NIC | Aggregate of all workers |
| Max connections | Limited by single machine OS | Sum of all workers |
| Deployment | Single binary | Coordinator + multiple workers |
| Result aggregation | Single machine stats | Cross-machine aggregation |

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

Same as original go-wrk - see LICENSE file.
