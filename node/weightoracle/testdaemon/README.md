# Mock Weight Oracle Daemon

A Python mock daemon for testing the weight oracle client integration.

## Requirements

- Python 3.10+ (uses `typing` features like `dict[str, int]`)
- No external dependencies (stdlib only)

## Usage

### Basic Usage

Start the daemon on a specific port:

```bash
python daemon.py --port 9876
```

### With Custom Genesis Hash

Provide a 32-byte genesis hash as hex (64 characters):

```bash
python daemon.py --port 9876 \
    --genesis-hash 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
```

Or as base64:

```bash
python daemon.py --port 9876 \
    --genesis-hash ASNFZ4mrze8BI0VniavN7wEjRWeJq83vASNFZ4mrze8=
```

### With Version Strings

Configure the protocol and algorithm versions:

```bash
python daemon.py --port 9876 \
    --protocol-version "2.0" \
    --algorithm-version "3.0"
```

### With Latency Simulation

Add artificial latency to simulate slow network/processing:

```bash
python daemon.py --port 9876 --latency 0.5
```

### With Weight Table

Load weights from a JSON file:

```bash
python daemon.py --port 9876 --weight-file weights.json
```

Weight table format:

```json
{
    "weights": {
        "ADDR1BASE32:selectid_hex:100": 50000,
        "ADDR2BASE32:selectid_hex:100": 75000
    }
}
```

Key format: `address:selection_id:balance_round`

## HTTP REST Protocol

The daemon implements the weight oracle protocol over HTTP REST.

### Endpoints

All endpoints accept POST requests with JSON body and return JSON responses.

| Endpoint | Request Body | Success Response |
|----------|--------------|------------------|
| `POST /ping` | `{}` | `{"pong":true}` |
| `POST /identity` | `{}` | `{"genesis_hash":"<base64>","protocol_version":"<str>","algorithm_version":"<str>"}` |
| `POST /weight` | `{"address":"<base32>","selection_id":"<hex>","balance_round":"<decimal>"}` | `{"weight":"<decimal>"}` |
| `POST /total_weight` | `{"balance_round":"<decimal>","vote_round":"<decimal>"}` | `{"total_weight":"<decimal>"}` |

### Error Response

All errors return JSON (never HTML):

```json
{"error":"<message>","code":"<code>"}
```

Error codes and HTTP status:
- `bad_request` (400): Invalid JSON or missing required fields
- `not_found` (404): Unknown endpoint
- `internal` (500): Internal server error

## Testing with curl

```bash
# Ping
curl -X POST http://localhost:9876/ping -H "Content-Type: application/json" -d '{}'

# Identity
curl -X POST http://localhost:9876/identity -H "Content-Type: application/json" -d '{}'

# Weight query
curl -X POST http://localhost:9876/weight -H "Content-Type: application/json" \
    -d '{"address":"ABC123","selection_id":"0123456789abcdef","balance_round":"100"}'

# Total weight query
curl -X POST http://localhost:9876/total_weight -H "Content-Type: application/json" \
    -d '{"balance_round":"100","vote_round":"105"}'
```

## Testing with the Go Client

```go
package main

import (
    "fmt"
    "github.com/algorand/go-algorand/node/weightoracle"
)

func main() {
    client := weightoracle.NewClient(9876)

    // Ping
    if err := client.Ping(); err != nil {
        fmt.Printf("Ping failed: %v\n", err)
        return
    }
    fmt.Println("Ping successful!")

    // Identity
    identity, err := client.Identity()
    if err != nil {
        fmt.Printf("Identity failed: %v\n", err)
        return
    }
    fmt.Printf("Genesis: %v, Protocol: %s, Algorithm: %s\n",
        identity.GenesisHash, identity.WeightProtocolVersion, identity.WeightAlgorithmVersion)
}
```

## Programmatic Usage

The daemon can also be used as a library for integration tests:

```python
from daemon import WeightDaemon
import threading

# Create daemon
daemon = WeightDaemon(
    port=9876,
    genesis_hash=bytes(32),
    protocol_version="1.0",
    algorithm_version="1.0",
)

# Set specific weights
daemon.set_weight("ADDR1", "selectid", "100", 50000)
daemon.set_total_weight(1000000)

# Start in background thread
thread = threading.Thread(target=daemon.start, daemon=True)
thread.start()

# ... run tests ...

# Stop daemon
daemon.stop()
```
