package orchestrator

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"sync"
	"time"
)

var ErrCircuitOpen = errors.New("circuit breaker open")

type cbState int

const (
	cbClosed   cbState = iota
	cbOpen
	cbHalfOpen
)

// CircuitBreaker is a per-key state machine: closed → open → half-open → closed.
type CircuitBreaker struct {
	mu              sync.Mutex
	state           cbState
	failures        int
	threshold       int
	recoveryTimeout time.Duration
	lastFailure     time.Time
	halfOpenMax     int
	halfOpenCalls   int
}

func newCircuitBreaker(threshold int, recovery time.Duration) *CircuitBreaker {
	if threshold <= 0 { threshold = 5 }
	if recovery <= 0 { recovery = 30 * time.Second }
	return &CircuitBreaker{threshold: threshold, recoveryTimeout: recovery, halfOpenMax: 1}
}

// Allow returns nil if the request may proceed, ErrCircuitOpen otherwise.
func (cb *CircuitBreaker) Allow() error {
	if cb == nil { return nil }
	cb.mu.Lock(); defer cb.mu.Unlock()
	switch cb.state {
	case cbClosed:
		return nil
	case cbOpen:
		if time.Since(cb.lastFailure) >= cb.recoveryTimeout {
			cb.state = cbHalfOpen; cb.halfOpenCalls = 0
			return nil
		}
		return ErrCircuitOpen
	case cbHalfOpen:
		if cb.halfOpenCalls < cb.halfOpenMax { cb.halfOpenCalls++; return nil }
		return ErrCircuitOpen
	}
	return ErrCircuitOpen
}

// RecordSuccess resets the breaker to closed.
func (cb *CircuitBreaker) RecordSuccess() {
	if cb == nil { return }
	cb.mu.Lock(); cb.failures = 0; cb.state = cbClosed; cb.halfOpenCalls = 0; cb.mu.Unlock()
}

// RecordFailure increments failures; opens the breaker when threshold is reached.
func (cb *CircuitBreaker) RecordFailure() {
	if cb == nil { return }
	cb.mu.Lock(); defer cb.mu.Unlock()
	cb.failures++; cb.lastFailure = time.Now()
	if cb.state == cbHalfOpen || cb.failures >= cb.threshold { cb.state = cbOpen }
}

// State returns the current breaker state as a string.
func (cb *CircuitBreaker) State() string {
	if cb == nil { return "closed" }
	cb.mu.Lock(); defer cb.mu.Unlock()
	switch cb.state {
	case cbClosed: return "closed"
	case cbOpen: return "open"
	case cbHalfOpen: return "half-open"
	}
	return "unknown"
}

// CircuitBreakerPool manages per-upstream-key circuit breakers.
type CircuitBreakerPool struct {
	mu              sync.Mutex
	breakers        map[string]*CircuitBreaker
	threshold       int
	recoveryTimeout time.Duration
}

func newCircuitBreakerPool(threshold int, recovery time.Duration) *CircuitBreakerPool {
	return &CircuitBreakerPool{breakers: map[string]*CircuitBreaker{}, threshold: threshold, recoveryTimeout: recovery}
}

// Get returns the circuit breaker for the given key, creating one if needed.
func (p *CircuitBreakerPool) Get(key string) *CircuitBreaker {
	if p == nil { return nil }
	p.mu.Lock(); defer p.mu.Unlock()
	if cb, ok := p.breakers[key]; ok { return cb }
	cb := newCircuitBreaker(p.threshold, p.recoveryTimeout)
	p.breakers[key] = cb
	return cb
}

// RetryWithBackoff retries fn up to maxRetries times with exponential backoff + jitter.
// Only retries on transient errors (see IsTransient). Returns nil on first success.
func RetryWithBackoff(ctx context.Context, maxRetries int, baseDelay time.Duration, fn func() error) error {
	if maxRetries <= 0 { return fn() }
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		lastErr = fn()
		if lastErr == nil { return nil }
		if !IsTransient(lastErr) || attempt == maxRetries { return lastErr }
		delay := time.Duration(float64(baseDelay) * math.Pow(2, float64(attempt)))
		jitter := time.Duration(rand.Int63n(int64(delay)/2 + 1))
		delay += jitter
		t := time.NewTimer(delay)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop(); return ctx.Err()
		}
	}
	return lastErr
}

// IsTransient returns true for errors that are safe to retry (network / 5xx-class).
func IsTransient(err error) bool {
	if err == nil { return false }
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) { return false }
	if errors.Is(err, ErrCircuitOpen) { return false }
	return true
}

// WithStageTimeout returns a child context cancelled after the given duration.
func WithStageTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 { return parent, func() {} }
	return context.WithTimeout(parent, timeout)
}
