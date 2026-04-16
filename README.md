# LiveFXHub Pricing Service

This is the ultra-low-latency Pricing Engine written in Go.

## Architecture
- **Ingestion**: Listens to QuickFIX market data (directly loads prices to RAM).
- **Processing**: A math pipeline takes the raw tick, fetches spread definitions from memory, and produces a single "Fat Payload" for all symbols.
- **Egress**: 
  - Broadcasts flat JSON payloads to ZeroMQ on `0.0.0.0:5556` for production quote compatibility.
  - Broadcasts packed JSON payloads to Redis Cluster using a Lua script for simultaneous `HSET` and `PUBLISH`.

## Deployment Scenarios
This microservice can be deployed either standalone via Systemctl or as part of the Kubernetes (K3s) cluster.

### Scenario A: Standalone via Systemctl (Production Recommended for Zero Bottleneck)
When running securely inside a dedicated environment (e.g. EC2, bare-metal server):

1. **Prerequisites**:
   - Install Go (>=1.21), `zeromq-dev`, `g++`, `gcc`, `musl-dev`.
   - On Ubuntu: `sudo apt install golang build-essential libzmq3-dev`
   
2. **Build**:
   ```bash
   cd /v3/pricing-service
   go mod tidy
   go build -o pricing-service-bin cmd/pricing-service/main.go
   ```
2.1 to run directly - go run ./cmd/pricing-service
3. **Configure Settings**:
   Edit or create `/v3/pricing-service/.env`:
   ```ini
   DATABASE_URL="postgres://livefxhubv3:Livefxhub@123@10.10.0.1:30432/user_db?sslmode=disable"
   REDIS_NODES="185.131.54.146:31010,185.131.54.146:31011,185.131.54.146:31003,185.131.54.146:31009,185.131.54.146:31007,185.131.54.146:31008"
   ZMQ_BIND="tcp://0.0.0.0:5556"
   # Wireguard IP connections work out of the box so long as the URLs point to those IP addresses.
   ```

4. **Install Systemd Service**:
   ```bash
   sudo cp pricing-service.service /etc/systemd/system/
   sudo systemctl daemon-reload
   sudo systemctl enable pricing-service
   sudo systemctl start pricing-service
   ```

5. **Logs**:
   `sudo journalctl -u pricing-service -f`

### Scenario B: K3s Cluster Deployment (Local network)
When deployed inside K3s, the CI/CD pipeline builds the Docker image and publishes it.

1. The service uses internal K8s DNS to talk to Redis and PostgreSQL.
2. The GitHub action located at `.github/workflows/ci.yml` handles automatic compilation via `docker build` using the provided Dockerfile.
3. K3s manifests are in `/v3/k8s/pricing-service-deployment.yaml`.





# Rebuild
go build -o pricing-service-bin ./cmd/pricing-service

# Copy binary + config + env to /opt
sudo cp pricing-service-bin /opt/pricing-service/
sudo cp -r config/ /opt/pricing-service/config/
sudo cp .env /opt/pricing-service/.env

# Restart
sudo systemctl restart pricing-service
sudo journalctl -u pricing-service -f
