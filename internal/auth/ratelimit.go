package auth

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// Login rate limiting (A4, M18), on top of the per-account lockout.
const (
	DefaultLoginRateLimit = 60 // attempts per address per window; generous because local browsers share one address
	rateWindow            = time.Minute
	maxTrackedAddresses   = 10000
)

var errTooManyAttempts = platform.NewError(http.StatusTooManyRequests, "too_many_attempts")

type attemptWindow struct {
	start  time.Time
	count  int
	warned bool
}

// attemptLimiter counts attempts per address in fixed one-minute windows.
type attemptLimiter struct {
	mu      sync.Mutex
	limit   int
	windows map[string]attemptWindow
}

func newAttemptLimiter(limit int) *attemptLimiter {
	return &attemptLimiter{limit: limit, windows: map[string]attemptWindow{}}
}

// allow records an attempt from key. A denied attempt reports how long to wait and whether it is
// the first denial in this window (so it is logged once). When the table is full of live windows,
// new addresses are refused: failing closed keeps the limit, the lockout still protects accounts.
func (l *attemptLimiter) allow(key string, now time.Time) (ok bool, retry time.Duration, firstDenial bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w, found := l.windows[key]
	if found && now.Sub(w.start) >= rateWindow {
		found = false
	}
	if !found {
		if len(l.windows) >= maxTrackedAddresses {
			for k, old := range l.windows {
				if now.Sub(old.start) >= rateWindow {
					delete(l.windows, k)
				}
			}
			if len(l.windows) >= maxTrackedAddresses {
				return false, rateWindow, false
			}
		}
		w = attemptWindow{start: now}
	}
	if w.count >= l.limit {
		firstDenial = !w.warned
		w.warned = true
		l.windows[key] = w
		return false, w.start.Add(rateWindow).Sub(now), firstDenial
	}
	w.count++
	l.windows[key] = w
	return true, 0, false
}

// clientKey is the connection's address: IPv4 as is, IPv6 by /64, which one client usually holds.
// shortcut: trusts the socket address only (X-Forwarded-For is ignored, the app is exposed
// directly); add a trusted-proxy setting if a reverse proxy is ever put in front.
func clientKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// limited answers 429 and returns true when the caller's address is over the limit.
func (s *Service) limited(w http.ResponseWriter, r *http.Request) bool {
	key := clientKey(r.RemoteAddr)
	ok, retry, first := s.attempts.allow(key, time.Now())
	if ok {
		return false
	}
	if first {
		s.log.Warn("sign-in attempts rate limited", "client", key, "path", r.URL.Path)
	}
	w.Header().Set("Retry-After", strconv.Itoa(int((retry+time.Second-1)/time.Second)))
	platform.WriteError(w, r, errTooManyAttempts)
	return true
}
