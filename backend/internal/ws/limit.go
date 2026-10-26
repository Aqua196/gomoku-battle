package ws

import (
	"net"
	"net/http"
	"strings"
	"time"
)

// bucket is a token bucket owned by one read pump (no locking needed):
// rate tokens per second refill up to burst; each inbound frame costs one.
type bucket struct {
	rate, burst float64
	tokens      float64
	last        time.Time
}

func newBucket(rate float64, burst int) *bucket {
	if rate <= 0 {
		return nil
	}
	b := float64(burst)
	if b < 1 {
		b = 1
	}
	return &bucket{rate: rate, burst: b, tokens: b}
}

// allow spends one token if available. A nil bucket allows everything.
func (b *bucket) allow(now time.Time) bool {
	if b == nil {
		return true
	}
	if !b.last.IsZero() {
		b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// clientIP is the address the per-IP connection cap counts against: the
// socket peer, or the first X-Forwarded-For hop when the server sits behind
// a trusted reverse proxy (the header is client-controlled otherwise).
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			if ip := strings.TrimSpace(first); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// acquireIP reserves one connection slot for ip; false means the cap is hit.
func (s *Server) acquireIP(ip string) bool {
	if s.cfg.MaxConnsPerIP <= 0 {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perIP[ip] >= s.cfg.MaxConnsPerIP {
		return false
	}
	s.perIP[ip]++
	return true
}

func (s *Server) releaseIP(ip string) {
	if s.cfg.MaxConnsPerIP <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perIP[ip]--; s.perIP[ip] <= 0 {
		delete(s.perIP, ip)
	}
}
