/*
 * Sample Trading Engine — C++17
 * In-memory limit order book with REST API.
 * Used for testing the Distributed Benchmark Platform.
 *
 * GET  /health → {"status":"ok"}
 * POST /order  → ack JSON
 */

#include "httplib.h"
#include <nlohmann/json.hpp>

#include <algorithm>
#include <atomic>
#include <chrono>
#include <cstdint>
#include <mutex>
#include <string>
#include <vector>

using json = nlohmann::json;

// ── Helpers ───────────────────────────────────────────────────────────────────

static std::string gen_id() {
    static std::atomic<uint64_t> n{0};
    return "ord-" + std::to_string(n.fetch_add(1, std::memory_order_relaxed));
}

static int64_t now_us() {
    using namespace std::chrono;
    return duration_cast<microseconds>(steady_clock::now().time_since_epoch()).count();
}

// ── Order Book ────────────────────────────────────────────────────────────────

struct Order {
    std::string id;
    std::string side;
    double      price;
    int         remaining;
    int64_t     ts;
};

class OrderBook {
public:
    json process(const json& req) {
        std::string type = req.value("type", "limit");
        std::string oid  = req.value("id",   gen_id());
        std::string side = req.value("side", "buy");
        double      price = req.value("price", 0.0);
        int         qty   = req.value("quantity", 1);

        if (type == "cancel") return do_cancel(oid);

        std::lock_guard<std::mutex> g(mu_);
        if (type == "market") return do_market(oid, side, qty);
        return do_limit(oid, side, price, qty);
    }

private:
    std::mutex         mu_;
    std::vector<Order> bids_; // highest price first
    std::vector<Order> asks_; // lowest price first

    // ── Limit order: try to match, rest goes on book ──────────────────────────
    json do_limit(const std::string& oid, const std::string& side,
                  double price, int qty) {
        int    filled = 0;
        double fp     = 0.0;

        if (side == "buy") {
            while (qty > 0 && !asks_.empty() && asks_.front().price <= price) {
                auto& top = asks_.front();
                int m = std::min(qty, top.remaining);
                fp = top.price; filled += m; qty -= m; top.remaining -= m;
                if (top.remaining == 0) asks_.erase(asks_.begin());
            }
            if (qty > 0) insert_sorted(bids_, {oid, side, price, qty, now_us()}, true);
        } else {
            while (qty > 0 && !bids_.empty() && bids_.front().price >= price) {
                auto& top = bids_.front();
                int m = std::min(qty, top.remaining);
                fp = top.price; filled += m; qty -= m; top.remaining -= m;
                if (top.remaining == 0) bids_.erase(bids_.begin());
            }
            if (qty > 0) insert_sorted(asks_, {oid, side, price, qty, now_us()}, false);
        }

        const char* st = (filled > 0 && qty == 0) ? "filled"
                       : (filled > 0)              ? "partial"
                                                   : "accepted";
        return {{"order_id", oid}, {"status", st},
                {"fill_price", fp}, {"fill_qty", filled}};
    }

    // ── Market order: sweep best available liquidity ──────────────────────────
    json do_market(const std::string& oid, const std::string& side, int qty) {
        auto& book = (side == "buy") ? asks_ : bids_;
        int    filled = 0;
        double fp     = 0.0;

        while (qty > 0 && !book.empty()) {
            auto& top = book.front();
            int m = std::min(qty, top.remaining);
            fp = top.price; filled += m; qty -= m; top.remaining -= m;
            if (top.remaining == 0) book.erase(book.begin());
        }

        return {{"order_id", oid},
                {"status",   filled > 0 ? "filled" : "rejected"},
                {"fill_price", fp}, {"fill_qty", filled}};
    }

    // ── Cancel: lazy linear scan (sufficient for a benchmark sample) ──────────
    json do_cancel(const std::string& oid) {
        std::lock_guard<std::mutex> g(mu_);
        auto erase_from = [&](std::vector<Order>& book) -> bool {
            auto it = std::find_if(book.begin(), book.end(),
                [&](const Order& o) { return o.id == oid; });
            if (it == book.end()) return false;
            book.erase(it);
            return true;
        };
        if (erase_from(bids_) || erase_from(asks_))
            return {{"order_id", oid}, {"status", "cancelled"},
                    {"fill_price", 0}, {"fill_qty", 0}};
        return {{"order_id", oid}, {"status", "rejected"},
                {"fill_price", 0}, {"fill_qty", 0}};
    }

    // Insert maintaining price-time priority.
    // descending=true → bids (highest price first); false → asks (lowest first).
    static void insert_sorted(std::vector<Order>& book, Order o, bool descending) {
        auto it = std::lower_bound(book.begin(), book.end(), o,
            [descending](const Order& a, const Order& b) {
                if (a.price != b.price)
                    return descending ? a.price > b.price : a.price < b.price;
                return a.ts < b.ts; // FIFO within same price level
            });
        book.insert(it, std::move(o));
    }
};

// ── Main ──────────────────────────────────────────────────────────────────────

int main() {
    OrderBook book;
    httplib::Server svr;

    svr.Get("/health", [](const httplib::Request&, httplib::Response& res) {
        res.set_content(R"({"status":"ok"})", "application/json");
    });

    svr.Post("/order", [&book](const httplib::Request& req, httplib::Response& res) {
        try {
            auto ack = book.process(json::parse(req.body));
            res.set_content(ack.dump(), "application/json");
        } catch (...) {
            res.status = 400;
            res.set_content(R"({"error":"invalid JSON"})", "application/json");
        }
    });

    const int port = 8080;
    printf("Sample trading engine (C++) listening on :%d\n", port);
    svr.listen("0.0.0.0", port);
}
