#!/usr/bin/env python3
# Copyright (C) 2019-2026 Algorand, Inc.
# This file is part of go-algorand
#
# go-algorand is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as
# published by the Free Software Foundation, either version 3 of the
# License, or (at your option) any later version.
#
# go-algorand is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with go-algorand.  If not, see <https://www.gnu.org/licenses/>.

"""
Mock Weight Oracle Daemon for Testing

A minimal TCP server that simulates the weight oracle daemon for integration testing.
Supports configurable responses for weight, total_weight, ping, and identity queries.

Wire Protocol:
- Each request is a single JSON object sent over TCP
- The daemon replies with a single JSON object and closes the connection
- Numeric values are decimal strings (not JSON numbers)

Request formats:
    ping:         {"type":"ping"}
    identity:     {"type":"identity"}
    weight:       {"type":"weight","address":"<base32>","selection_id":"<hex>","balance_round":"<decimal>"}
    total_weight: {"type":"total_weight","balance_round":"<decimal>","vote_round":"<decimal>"}

Success responses:
    ping:         {"pong":true}
    identity:     {"genesis_hash":"<base64>","protocol_version":"<str>","algorithm_version":"<str>"}
    weight:       {"weight":"<decimal>"}
    total_weight: {"total_weight":"<decimal>"}

Error response:
    {"error":"<message>","code":"<code>"}
    Codes: "not_found", "bad_request", "internal", "unsupported"
"""

import argparse
import base64
import json
import socket
import sys
import threading
import time
from typing import Any


class WeightDaemon:
    """Mock weight oracle daemon for testing."""

    def __init__(
        self,
        port: int,
        genesis_hash: bytes,
        protocol_version: str = "1.0",
        algorithm_version: str = "1.0",
        latency: float = 0.0,
        weight_table: dict[str, int] | None = None,
        total_weight: int = 1000000,
        default_weight: int | None = None,
        address_weights: dict[str, int] | None = None,
    ):
        """
        Initialize the mock daemon.

        Args:
            port: TCP port to listen on
            genesis_hash: 32-byte genesis hash (will be base64 encoded in responses)
            protocol_version: Weight protocol version string
            algorithm_version: Weight algorithm version string
            latency: Artificial latency to add to each response (seconds)
            weight_table: Dict mapping "address:selection_id:balance_round" to weight
            total_weight: Default total weight to return
            default_weight: If set, return this weight for all queries (bypasses table lookup)
            address_weights: Dict mapping just address to weight (simpler lookup, ignores selection_id/round)
        """
        self.port = port
        self.genesis_hash = genesis_hash
        self.protocol_version = protocol_version
        self.algorithm_version = algorithm_version
        self.latency = latency
        self.weight_table = weight_table or {}
        self.total_weight = total_weight
        self.default_weight = default_weight
        self.address_weights = address_weights or {}
        self.running = False
        self.server_socket: socket.socket | None = None
        self._lock = threading.Lock()

    def start(self) -> None:
        """Start the daemon server."""
        self.server_socket = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.server_socket.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.server_socket.bind(("127.0.0.1", self.port))
        self.server_socket.listen(10)
        self.running = True

        print(f"Weight daemon listening on 127.0.0.1:{self.port}", file=sys.stderr)

        while self.running:
            try:
                self.server_socket.settimeout(1.0)  # Allow periodic check of running flag
                try:
                    client_socket, addr = self.server_socket.accept()
                except socket.timeout:
                    continue

                # Handle each connection in a new thread
                thread = threading.Thread(
                    target=self._handle_client, args=(client_socket,), daemon=True
                )
                thread.start()

            except Exception as e:
                if self.running:
                    print(f"Error accepting connection: {e}", file=sys.stderr)

    def stop(self) -> None:
        """Stop the daemon server."""
        self.running = False
        if self.server_socket:
            self.server_socket.close()

    def _handle_client(self, client_socket: socket.socket) -> None:
        """Handle a single client connection."""
        try:
            # Apply latency if configured
            if self.latency > 0:
                time.sleep(self.latency)

            # Read the request
            data = b""
            while True:
                chunk = client_socket.recv(4096)
                if not chunk:
                    break
                data += chunk
                # JSON objects end with } so we can detect end of message
                if b"}" in data:
                    break

            if not data:
                return

            # Parse and handle the request
            try:
                request = json.loads(data.decode("utf-8"))
            except json.JSONDecodeError as e:
                response = {"error": f"Invalid JSON: {e}", "code": "bad_request"}
                self._send_response(client_socket, response)
                return

            response = self._handle_request(request)
            self._send_response(client_socket, response)

        except Exception as e:
            print(f"Error handling client: {e}", file=sys.stderr)
        finally:
            client_socket.close()

    def _send_response(
        self, client_socket: socket.socket, response: dict[str, Any]
    ) -> None:
        """Send a JSON response to the client."""
        try:
            response_json = json.dumps(response) + "\n"
            client_socket.sendall(response_json.encode("utf-8"))
        except Exception as e:
            print(f"Error sending response: {e}", file=sys.stderr)

    def _handle_request(self, request: dict[str, Any]) -> dict[str, Any]:
        """Handle a request and return the appropriate response."""
        req_type = request.get("type", "")

        if req_type == "ping":
            return self._handle_ping()
        elif req_type == "identity":
            return self._handle_identity()
        elif req_type == "weight":
            return self._handle_weight(request)
        elif req_type == "total_weight":
            return self._handle_total_weight(request)
        else:
            return {"error": f"Unknown request type: {req_type}", "code": "unsupported"}

    def _handle_ping(self) -> dict[str, Any]:
        """Handle a ping request."""
        return {"pong": True}

    def _handle_identity(self) -> dict[str, Any]:
        """Handle an identity request."""
        return {
            "genesis_hash": base64.b64encode(self.genesis_hash).decode("ascii"),
            "protocol_version": self.protocol_version,
            "algorithm_version": self.algorithm_version,
        }

    def _handle_weight(self, request: dict[str, Any]) -> dict[str, Any]:
        """Handle a weight request."""
        # Validate required fields
        address = request.get("address")
        selection_id = request.get("selection_id")
        balance_round = request.get("balance_round")

        if not address:
            return {"error": "Missing address field", "code": "bad_request"}
        if not selection_id:
            return {"error": "Missing selection_id field", "code": "bad_request"}
        if not balance_round:
            return {"error": "Missing balance_round field", "code": "bad_request"}

        # If default_weight is set, return it for all queries (bypasses table lookup)
        if self.default_weight is not None:
            return {"weight": str(self.default_weight)}

        with self._lock:
            # First check address_weights (simple address-only lookup)
            # This is the preferred method for testing weighted consensus
            if address in self.address_weights:
                return {"weight": str(self.address_weights[address])}

            # Fall back to full key lookup in weight_table
            key = f"{address}:{selection_id}:{balance_round}"
            if key in self.weight_table:
                weight = self.weight_table[key]
            else:
                # Default behavior: return a weight based on address hash for consistency
                # This allows testing without a full weight table
                weight = sum(ord(c) for c in address) % 1000000

        return {"weight": str(weight)}

    def _handle_total_weight(self, request: dict[str, Any]) -> dict[str, Any]:
        """Handle a total_weight request."""
        balance_round = request.get("balance_round")
        vote_round = request.get("vote_round")

        if not balance_round:
            return {"error": "Missing balance_round field", "code": "bad_request"}
        if not vote_round:
            return {"error": "Missing vote_round field", "code": "bad_request"}

        return {"total_weight": str(self.total_weight)}

    def set_weight(self, address: str, selection_id: str, balance_round: str, weight: int) -> None:
        """Set a specific weight in the weight table (thread-safe)."""
        key = f"{address}:{selection_id}:{balance_round}"
        with self._lock:
            self.weight_table[key] = weight

    def set_total_weight(self, total_weight: int) -> None:
        """Set the total weight returned by total_weight queries (thread-safe)."""
        with self._lock:
            self.total_weight = total_weight


def load_weight_table(filename: str) -> dict[str, int]:
    """
    Load a weight table from a JSON file.

    Expected format:
    {
        "weights": {
            "address1:selection_id1:round1": 1000,
            "address2:selection_id2:round2": 2000
        }
    }
    """
    with open(filename, "r") as f:
        data = json.load(f)
    return data.get("weights", {})


def load_address_weights(filename: str) -> dict[str, int]:
    """
    Load address weights from a JSON file.

    Expected format:
    {
        "ADDRESS1...": 1000000,
        "ADDRESS2...": 1500000
    }

    This is simpler than the full weight table - just maps addresses to weights.
    Used for testing weighted consensus where all nodes need the same view of weights.
    """
    with open(filename, "r") as f:
        return json.load(f)


def parse_genesis_hash(genesis_hash_str: str) -> bytes:
    """Parse genesis hash from hex or base64 string."""
    # Try hex first (64 chars for 32 bytes)
    if len(genesis_hash_str) == 64:
        try:
            return bytes.fromhex(genesis_hash_str)
        except ValueError:
            pass

    # Try base64
    try:
        decoded = base64.b64decode(genesis_hash_str)
        if len(decoded) == 32:
            return decoded
    except Exception:
        pass

    raise ValueError(
        f"Invalid genesis hash: expected 32-byte hex (64 chars) or base64 string, got {genesis_hash_str!r}"
    )


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Mock Weight Oracle Daemon for Testing",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
    # Start with default settings (random genesis hash)
    python daemon.py --port 9876

    # Start with specific genesis hash (hex)
    python daemon.py --port 9876 --genesis-hash 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef

    # Start with latency simulation
    python daemon.py --port 9876 --latency 0.5

    # Start with weight table from file
    python daemon.py --port 9876 --weight-file weights.json

    # Start with fixed weight for all queries (useful for testing weighted consensus)
    python daemon.py --port 9876 --default-weight 1000000 --total-weight 5500000

    # Test with netcat
    echo '{"type":"ping"}' | nc localhost 9876
    echo '{"type":"identity"}' | nc localhost 9876
""",
    )

    parser.add_argument(
        "--port",
        type=int,
        required=True,
        help="TCP port to listen on",
    )
    parser.add_argument(
        "--genesis-hash",
        type=str,
        default=None,
        help="32-byte genesis hash as hex (64 chars) or base64. Default: all zeros",
    )
    parser.add_argument(
        "--protocol-version",
        type=str,
        default="1.0",
        help="Weight protocol version to report (default: 1.0)",
    )
    parser.add_argument(
        "--algorithm-version",
        type=str,
        default="1.0",
        help="Weight algorithm version to report (default: 1.0)",
    )
    parser.add_argument(
        "--latency",
        type=float,
        default=0.0,
        help="Artificial latency in seconds to add to each response (default: 0)",
    )
    parser.add_argument(
        "--weight-file",
        type=str,
        default=None,
        help="JSON file containing weight table",
    )
    parser.add_argument(
        "--total-weight",
        type=int,
        default=1000000,
        help="Default total weight to return (default: 1000000)",
    )
    parser.add_argument(
        "--default-weight",
        type=int,
        default=None,
        help="If set, return this weight for all queries (bypasses table lookup)",
    )
    parser.add_argument(
        "--address-weights-file",
        type=str,
        default=None,
        help="JSON file mapping addresses to weights (simpler than --weight-file)",
    )

    args = parser.parse_args()

    # Parse or generate genesis hash
    if args.genesis_hash:
        genesis_hash = parse_genesis_hash(args.genesis_hash)
    else:
        # Default: 32 zero bytes
        genesis_hash = bytes(32)

    # Load weight table if specified
    weight_table = {}
    if args.weight_file:
        try:
            weight_table = load_weight_table(args.weight_file)
            print(f"Loaded {len(weight_table)} weights from {args.weight_file}", file=sys.stderr)
        except Exception as e:
            print(f"Error loading weight file: {e}", file=sys.stderr)
            sys.exit(1)

    # Load address weights if specified (simpler format, just address -> weight)
    address_weights = {}
    if args.address_weights_file:
        try:
            address_weights = load_address_weights(args.address_weights_file)
            print(f"Loaded {len(address_weights)} address weights from {args.address_weights_file}", file=sys.stderr)
        except Exception as e:
            print(f"Error loading address weights file: {e}", file=sys.stderr)
            sys.exit(1)

    # Create and start daemon
    daemon = WeightDaemon(
        port=args.port,
        genesis_hash=genesis_hash,
        protocol_version=args.protocol_version,
        algorithm_version=args.algorithm_version,
        latency=args.latency,
        weight_table=weight_table,
        total_weight=args.total_weight,
        default_weight=args.default_weight,
        address_weights=address_weights,
    )

    try:
        daemon.start()
    except KeyboardInterrupt:
        print("\nShutting down...", file=sys.stderr)
        daemon.stop()


if __name__ == "__main__":
    main()
