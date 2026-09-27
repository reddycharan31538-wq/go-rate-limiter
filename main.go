package main

import (
    "context"
    "fmt"
    "net/http"
    "time"

    "github.com/go-redis/redis/v8"
)

var ctx = context.Background()
var rdb *redis.Client
var rateLimiterScript *redis.Script

func initRedis() {
    rdb = redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })

    luaCode := `
        local key = KEYS[1]
        local capacity = tonumber(ARGV[1])
        local refill_rate = tonumber(ARGV[2])
        local now = tonumber(ARGV[3])

        local data = redis.call("HMGET", key, "tokens", "last_updated")
        local tokens = tonumber(data[1])
        local last_updated = tonumber(data[2])

        if not tokens then
            tokens = capacity
            last_updated = now
        else
            local delta = math.max(0, now - last_updated)
            tokens = math.min(capacity, tokens + (delta * refill_rate))
            last_updated = now
        end

        if tokens >= 1 then
            tokens = tokens - 1
            redis.call("HMSET", key, "tokens", tokens, "last_updated", last_updated)
            redis.call("EXPIRE", key, 60)
            return 1
        else
            return 0
        end
    `
    rateLimiterScript = redis.NewScript(luaCode)
}

func rateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        clientIP := r.RemoteAddr
        key := fmt.Sprintf("rate_limit:%s", clientIP)
        now := time.Now().Unix()

        res, err := rateLimiterScript.Run(ctx, rdb, []string{key}, 5, 1, now).Result()
        if err != nil {
            http.Error(w, "Internal Server Error", http.StatusInternalServerError)
            return
        }

        if res.(int64) == 0 {
            w.WriteHeader(http.StatusTooManyRequests)
            w.Write([]byte("429 Too Many Requests - Rate Limit Exceeded!\n"))
            return
        }

        next(w, r)
    }
}

func main() {
    initRedis()
    http.HandleFunc("/api/data", rateLimitMiddleware(func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("200 OK - Access Granted!\n"))
    }))

    fmt.Println("Server running on http://localhost:8080...")
    http.ListenAndServe(":8080", nil)
}
