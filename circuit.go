package circuit

import (
	"errors"
	"sync/atomic"
	"time"
)

// ToState makes a decision if a transition to the other state needs to be done
// based on counts total, failures.
type ToState func(uint32, uint32) bool

const (
	closed   = int32(0) // the request from the application is allowed to pass
	halfOpen = int32(1) // a limited number of requests are allowed to pass
	open     = int32(2) // the request is failed immediately and ErrBreakerOpen returned
)

// ErrBreakerOpen is returned from Execute when the breaker is not ready,
// use it to distinguish from request's errors.
var ErrBreakerOpen = errors.New("circuit: breaker open")

type Breaker struct {
	state int32 // current state
	until int64 // until timestamp of the interval (in closed state) or cooldown (in open state) period

	interval    int64  // the cyclic period of the closed state
	cooldown    int64  // the period of the open state
	atLeastReqs uint32 // # of requests in the half-open state

	toOpenState   ToState // called on failure being in the closed state
	toClosedState ToState // called after atLeastReqs being in the half-open state

	total    uint32 // # of requests in total during the interval
	failures uint32 // # of requests returned an error during the interval

	now func() time.Time // time.Now
}

// NewBreaker returns a new circuit breaker,
// it can prevent an application from repeatedly trying
// to execute an operation that's likely to fail.
//
// Interval is the cyclic period of the closed state.
//
// Cooldown is the period of the open state,
// after which the state of the circuit breaker becomes the half-open.
//
// AtLeastReqs is the number of requests to consider in the half-open state
// before invoking a given toClosed function for decision making.
//
// ToOpen is called whenever a request fails in the closed state.
// If it returns true, the circuit breaker will be placed into the open state.
//
// ToClosed is called in the half-open state once the number of requests reached atLeastReqs.
// If it returns true, the circuit breaker will be placed into the closed state,
// otherwise into the open state.
//
// A function signature of toOpen and toClosed:
//     func(total uint32, failures uint32) bool
func NewBreaker(interval time.Duration, cooldown time.Duration, atLeastReqs uint32, toOpen ToState, toClosed ToState) (*Breaker, error) {
	return withTimeNow(interval, cooldown, atLeastReqs, toOpen, toClosed, time.Now)
}

func withTimeNow(interval time.Duration, cooldown time.Duration, atLeastReqs uint32, toOpen ToState, toClosed ToState, now func() time.Time) (*Breaker, error) {
	if interval.Nanoseconds() == 0 {
		return nil, errors.New("circuit: interval must be set")
	}

	if cooldown.Nanoseconds() == 0 {
		return nil, errors.New("circuit: cooldown must be set")
	}

	if atLeastReqs == 0 {
		return nil, errors.New("circuit: atLeastReqs must be set")
	}

	if toOpen == nil {
		return nil, errors.New("circuit: toOpen must be defined")
	}

	if toClosed == nil {
		return nil, errors.New("circuit: toClosed must be defined")
	}

	b := &Breaker{
		state:         closed,
		until:         now().UnixNano() + interval.Nanoseconds(),
		interval:      interval.Nanoseconds(),
		cooldown:      cooldown.Nanoseconds(),
		atLeastReqs:   atLeastReqs,
		toOpenState:   toOpen,
		toClosedState: toClosed,
		now:           now,
	}
	return b, nil
}

// Execute runs a given request if the circuit breaker accepts it,
// cases when it's in the closed state, or half-open one
// and the number of requests has not yet reached `atLeastReqs`.
//
// Returns ErrBreakerOpen when it doesn't accept the request,
// otherwise the error from the req function.
func (b *Breaker) Execute(req func() error) error {
	if !b.ready() {
		return ErrBreakerOpen
	}

	atomic.AddUint32(&b.total, 1)
	err := req()

	if err != nil {
		atomic.AddUint32(&b.failures, 1)
		b.onFailure()
	}

	return err
}

func (b *Breaker) ready() bool {
	// any state changes are done based on CompareAndSwap(until)
	until := atomic.LoadInt64(&b.until)

	state := atomic.LoadInt32(&b.state)
	now := b.now().UnixNano()

	if state == closed {
		if now > until {
			// interval period elapsed
			if atomic.CompareAndSwapInt64(&b.until, until, now+b.interval) {
				atomic.StoreUint32(&b.failures, 0)
				atomic.StoreUint32(&b.total, 0)
			}
		}
		return true
	}

	if state == open {
		if now > until {
			// cooldown period elapsed
			if atomic.CompareAndSwapInt64(&b.until, until, now+b.interval) {
				atomic.StoreUint32(&b.failures, 0)
				atomic.StoreUint32(&b.total, 0)
				atomic.StoreInt32(&b.state, halfOpen)
				return true
			}
		}
		return false
	}

	// in halfOpen state
	total := atomic.LoadUint32(&b.total)
	failures := atomic.LoadUint32(&b.failures)
	atLeastReqs := atomic.LoadUint32(&b.atLeastReqs)

	if total < atLeastReqs {
		// there is still a room for the request in halfOpen state
		return true
	}

	if b.toClosedState(total, failures) {
		if atomic.CompareAndSwapInt64(&b.until, until, now+b.interval) {
			atomic.StoreUint32(&b.failures, 0)
			atomic.StoreUint32(&b.total, 0)
			atomic.StoreInt32(&b.state, closed)
		}
		return true
	}

	// didn't pass, back to the open state
	if atomic.CompareAndSwapInt64(&b.until, until, now+b.cooldown) {
		atomic.StoreUint32(&b.failures, 0)
		atomic.StoreUint32(&b.total, 0)
		atomic.StoreInt32(&b.state, open)
	}
	return false
}

func (b *Breaker) onFailure() {
	// any state changes are done based on CompareAndSwap(until)
	until := atomic.LoadInt64(&b.until)

	if atomic.LoadInt32(&b.state) != closed {
		return
	}

	total := atomic.LoadUint32(&b.total)
	failures := atomic.LoadUint32(&b.failures)

	if b.toOpenState(total, failures) {
		now := b.now().UnixNano()
		if atomic.CompareAndSwapInt64(&b.until, until, now+b.cooldown) {
			atomic.StoreUint32(&b.failures, 0)
			atomic.StoreUint32(&b.total, 0)
			atomic.StoreInt32(&b.state, open)
		}
	}
}
