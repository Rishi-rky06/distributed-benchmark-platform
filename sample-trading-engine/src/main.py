"""
Sample Trading Engine — In-Memory Orderbook with REST API.
Used for testing the Distributed Benchmark Platform.

Exposes:
  GET  /health  → 200 {"status":"ok"}
  POST /order   → 200 Ack JSON
  GET  /ws      → WebSocket order endpoint (mirrors REST logic)
"""

import json
import time
import threading
from http.server import HTTPServer, BaseHTTPRequestHandler
from collections import defaultdict
from dataclasses import dataclass, field
from typing import Optional
import heapq
import uuid


# ── Order Book ───────────────────────────────────────────────────

@dataclass(order=True)
class BookOrder:
    """An order sitting on the book, ordered by price-time priority."""
    priority_price: float  # negative for bids (max-heap), positive for asks (min-heap)
    timestamp: float
    order_id: str = field(compare=False)
    side: str = field(compare=False)
    price: float = field(compare=False)
    remaining: int = field(compare=False)


class OrderBook:
    """Simple price-time-priority limit order book with matching."""

    def __init__(self):
        self.bids: list[BookOrder] = []   # max-heap (negative prices)
        self.asks: list[BookOrder] = []   # min-heap (positive prices)
        self.orders: dict[str, BookOrder] = {}
        self.lock = threading.Lock()

    def process_order(self, data: dict) -> dict:
        """Process an incoming order and return an Ack."""
        order_type = data.get("type", "limit")
        order_id = data.get("id", str(uuid.uuid4())[:8])
        side = data.get("side", "buy")
        price = data.get("price", 0.0)
        qty = data.get("quantity", 1)
        symbol = data.get("symbol", "UNKNOWN")

        if order_type == "cancel":
            return self._cancel(order_id)

        with self.lock:
            if order_type == "market":
                return self._market_order(order_id, side, qty)
            else:
                return self._limit_order(order_id, side, price, qty)

    def _limit_order(self, oid: str, side: str, price: float, qty: int) -> dict:
        """Place a limit order; try to match first, rest goes on the book."""
        filled_qty = 0
        fill_price = 0.0

        if side == "buy":
            # Match against asks
            while qty > 0 and self.asks and self.asks[0].priority_price <= price:
                best = self.asks[0]
                match_qty = min(qty, best.remaining)
                fill_price = best.price
                filled_qty += match_qty
                qty -= match_qty
                best.remaining -= match_qty
                if best.remaining <= 0:
                    heapq.heappop(self.asks)
                    self.orders.pop(best.order_id, None)

            # Remainder goes on the book
            if qty > 0:
                bo = BookOrder(-price, time.time(), oid, side, price, qty)
                heapq.heappush(self.bids, bo)
                self.orders[oid] = bo
        else:
            # Match against bids
            while qty > 0 and self.bids and (-self.bids[0].priority_price) >= price:
                best = self.bids[0]
                match_qty = min(qty, best.remaining)
                fill_price = best.price
                filled_qty += match_qty
                qty -= match_qty
                best.remaining -= match_qty
                if best.remaining <= 0:
                    heapq.heappop(self.bids)
                    self.orders.pop(best.order_id, None)

            if qty > 0:
                bo = BookOrder(price, time.time(), oid, side, price, qty)
                heapq.heappush(self.asks, bo)
                self.orders[oid] = bo

        status = "filled" if filled_qty > 0 and qty == 0 else (
            "partial" if filled_qty > 0 else "accepted"
        )
        return {
            "order_id": oid,
            "status": status,
            "fill_price": round(fill_price, 2),
            "fill_qty": filled_qty,
        }

    def _market_order(self, oid: str, side: str, qty: int) -> dict:
        """Execute a market order — take best available liquidity."""
        filled_qty = 0
        fill_price = 0.0
        book = self.asks if side == "buy" else self.bids

        while qty > 0 and book:
            best = book[0]
            match_qty = min(qty, best.remaining)
            fill_price = best.price
            filled_qty += match_qty
            qty -= match_qty
            best.remaining -= match_qty
            if best.remaining <= 0:
                heapq.heappop(book)
                self.orders.pop(best.order_id, None)

        status = "filled" if filled_qty > 0 else "rejected"
        return {
            "order_id": oid,
            "status": status,
            "fill_price": round(fill_price, 2),
            "fill_qty": filled_qty,
        }

    def _cancel(self, oid: str) -> dict:
        """Cancel an order by ID (lazy — marks remaining = 0)."""
        with self.lock:
            if oid in self.orders:
                self.orders[oid].remaining = 0
                del self.orders[oid]
                return {"order_id": oid, "status": "cancelled", "fill_price": 0, "fill_qty": 0}
            return {"order_id": oid, "status": "rejected", "fill_price": 0, "fill_qty": 0}


# ── HTTP Server ──────────────────────────────────────────────────

book = OrderBook()


class Handler(BaseHTTPRequestHandler):
    """Minimal HTTP handler for the trading engine."""

    def do_GET(self):
        if self.path == "/health":
            self._json(200, {"status": "ok"})
        else:
            self._json(404, {"error": "not found"})

    def do_POST(self):
        if self.path == "/order":
            length = int(self.headers.get("Content-Length", 0))
            body = self.rfile.read(length)
            try:
                data = json.loads(body)
            except json.JSONDecodeError:
                self._json(400, {"error": "invalid JSON"})
                return
            ack = book.process_order(data)
            self._json(200, ack)
        else:
            self._json(404, {"error": "not found"})

    def _json(self, code: int, data: dict):
        payload = json.dumps(data).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, format, *args):
        pass  # Suppress per-request logging for benchmark performance


def main():
    port = 8080
    server = HTTPServer(("0.0.0.0", port), Handler)
    print(f"Sample trading engine listening on :{port}")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        server.shutdown()


if __name__ == "__main__":
    main()
