package testutil

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

// Faults wraps a real handler and injects failures into the next requests, in the order
// they were queued. It is safe for concurrent use.
type Faults struct {
	next     http.Handler
	mu       sync.Mutex
	queue    []func(http.ResponseWriter, *http.Request)
	requests int
}

func NewFaults(next http.Handler) *Faults { return &Faults{next: next} }

func (f *Faults) push(fault func(http.ResponseWriter, *http.Request)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, fault)
}

// FailNext answers the next n requests with status, without reaching the handler.
func (f *Faults) FailNext(n, status int) {
	for range n {
		f.push(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
	}
}

// DelayNext holds the next request for d (or until the client gives up), then serves it.
func (f *Faults) DelayNext(d time.Duration) {
	f.push(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(d):
			f.next.ServeHTTP(w, r)
		case <-r.Context().Done():
		}
	})
}

// MalformNext answers the next request with a truncated JSON body and status 200.
func (f *Faults) MalformNext() {
	f.push(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result": `))
	})
}

// RespondNext answers the next request with status and body, without reaching the handler.
func (f *Faults) RespondNext(status int, body string) {
	f.push(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	})
}

// Requests is how many requests reached Faults, faulted or not.
func (f *Faults) Requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

// ApplyThenFail lets the handler apply the next request, then answers 500: the lost response.
func (f *Faults) ApplyThenFail() {
	f.push(func(w http.ResponseWriter, r *http.Request) {
		f.next.ServeHTTP(httptest.NewRecorder(), r)
		w.WriteHeader(http.StatusInternalServerError)
	})
}

func (f *Faults) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests++
	var fault func(http.ResponseWriter, *http.Request)
	if len(f.queue) > 0 {
		fault, f.queue = f.queue[0], f.queue[1:]
	}
	f.mu.Unlock()
	if fault == nil {
		f.next.ServeHTTP(w, r)
		return
	}
	fault(w, r)
}
