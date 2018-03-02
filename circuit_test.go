package circuit

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewBreaker(t *testing.T) {
	to := func(uint32, uint32) bool { return false }
	_, err := NewBreaker(0, time.Minute, 100, to, to)
	assert.Error(t, err, "circuit: interval must be set")

	_, err = NewBreaker(time.Minute, time.Minute, 100, to, nil)
	assert.Error(t, err, "circuit: toClosed must be defined")

	b, err := NewBreaker(time.Minute, time.Minute, 100, to, to)
	assert.NoError(t, err)
	assert.Equal(t, closed, b.state)
}

func TestBreaker_OnFailure(t *testing.T) {
	toOpen := func(total uint32, failures uint32) bool {
		return total > 1 && failures > 1
	}

	toClosed := func(uint32, uint32) bool { return false }
	b, err := withTimeNow(time.Minute, 2*time.Minute, 100, toOpen, toClosed, now(1520100000))
	assert.NoError(t, err)
	assert.Equal(t, closed, b.state)
	assert.Equal(t, int64(1520100060000000000), b.until)

	b.total = 1
	b.failures = 1
	b.onFailure()
	assert.Equal(t, closed, b.state)
	assert.Equal(t, int64(1520100060000000000), b.until)

	b.total = 2
	b.failures = 2
	b.onFailure()
	assert.Equal(t, open, b.state)
	assert.Equal(t, int64(1520100120000000000), b.until)
}

func TestBreaker_Execute_WhenClosed(t *testing.T) {
	toOpen := func(total uint32, failures uint32) bool { return total > 0 && failures > 0 }
	toClosed := func(total uint32, failures uint32) bool { return false }
	b, err := withTimeNow(time.Minute, 2*time.Minute, 1, toOpen, toClosed, now(1520100000))
	assert.NoError(t, err)
	assert.Equal(t, int64(1520100060000000000), b.until)
	assert.Equal(t, uint32(0), b.total)
	assert.Equal(t, uint32(0), b.failures)

	err = b.Execute(func() error { return nil })
	assert.NoError(t, err)
	assert.Equal(t, uint32(1), b.total)
	assert.Equal(t, uint32(0), b.failures)

	err = b.Execute(func() error { return nil })
	assert.NoError(t, err)
	assert.Equal(t, uint32(2), b.total)
	assert.Equal(t, uint32(0), b.failures)

	// passed interval period, 61 sec
	b.now = now(1520100061)
	err = b.Execute(func() error { return nil })
	assert.NoError(t, err)
	assert.Equal(t, int64(1520100121000000000), b.until)
	assert.Equal(t, uint32(1), b.total)
	assert.Equal(t, uint32(0), b.failures)
}

func TestBreaker_Execute_WhenOpen(t *testing.T) {
	toOpen := func(total uint32, failures uint32) bool { return total > 0 && failures > 0 }
	toClosed := func(total uint32, failures uint32) bool { return false }
	b, err := withTimeNow(time.Minute, 2*time.Minute, 1, toOpen, toClosed, now(1520100000))
	assert.NoError(t, err)

	// open the breaker
	err = b.Execute(func() error { return errors.New("failed") })
	assert.Error(t, err, "failed")
	assert.Equal(t, open, b.state)
	assert.Equal(t, int64(1520100120000000000), b.until)

	// cooldown period, still open
	err = b.Execute(func() error { return nil })
	assert.Equal(t, ErrBreakerOpen, err)

	// after cooldown period (passed 121 sec)
	b.now = now(1520100121)
	err = b.Execute(func() error { return nil })
	assert.Equal(t, nil, err)
	assert.Equal(t, halfOpen, b.state)
	assert.Equal(t, int64(1520100181000000000), b.until)

	// atLeastReq exceeded, toClosed is invoked for the decision making
	b.toClosedState = func(total uint32, failures uint32) bool { return false }
	err = b.Execute(func() error { return nil })
	assert.Equal(t, ErrBreakerOpen, err)
	assert.Equal(t, open, b.state)
	assert.Equal(t, int64(1520100241000000000), b.until)

	// after the second cooldown period
	b.now = now(1520100242)
	err = b.Execute(func() error { return nil })
	assert.Equal(t, nil, err)
	assert.Equal(t, halfOpen, b.state)
	assert.Equal(t, int64(1520100302000000000), b.until)

	// atLeastReq exceeded, toClosed is invoked for the decision making
	b.now = now(1520100302)
	b.toClosedState = func(total uint32, failures uint32) bool { return true }
	err = b.Execute(func() error { return nil })
	assert.Equal(t, nil, err)
	assert.Equal(t, closed, b.state)
	assert.Equal(t, int64(1520100362000000000), b.until)
}

func TestBreaker_Execute_RequestsInParallel(t *testing.T) {
	toOpen := func(uint32, uint32) bool { return true }
	toClosed := func(uint32, uint32) bool { return false }
	b, err := withTimeNow(time.Minute, 2*time.Minute, 1, toOpen, toClosed, now(1520100000))
	assert.NoError(t, err)
	assert.Equal(t, int64(1520100060000000000), b.until)

	b.now = now(1520100001)

	var wg sync.WaitGroup
	wg.Add(20)
	for i := 0; i < 20; i++ {
		go func() {
			err = b.Execute(func() error { return nil })
			assert.NoError(t, err)
			wg.Done()
		}()
	}

	wg.Wait()
	assert.Equal(t, uint32(20), b.total)
	assert.Equal(t, uint32(0), b.failures)
	assert.Equal(t, int64(1520100060000000000), b.until)
}

func TestBreaker_Execute_FailuresInParallel(t *testing.T) {
	toOpen := func(uint32, uint32) bool { return true }
	toClosed := func(uint32, uint32) bool { return false }
	b, err := withTimeNow(time.Minute, 2*time.Minute, 1, toOpen, toClosed, now(1520100000))
	assert.NoError(t, err)
	assert.Equal(t, int64(1520100060000000000), b.until)
	assert.Equal(t, closed, b.state)

	b.now = now(1520100001)

	var wg sync.WaitGroup
	wg.Add(20)
	for i := 0; i < 20; i++ {
		go func() {
			err = b.Execute(func() error { return errors.New("failed") })
			assert.NotNil(t, err)
			wg.Done()
		}()
	}

	wg.Wait()
	assert.True(t, b.total < 20)
	assert.True(t, b.failures < 20)
	assert.Equal(t, open, b.state)
	assert.Equal(t, int64(1520100121000000000), b.until)
}

func TestBreaker_Execute_AtLeastReqsInParallel(t *testing.T) {
	toOpen := func(uint32, uint32) bool { return true }
	toClosed := func(uint32, uint32) bool { return true }
	b, err := withTimeNow(time.Minute, 2*time.Minute, 10, toOpen, toClosed, now(1520100000))
	assert.NoError(t, err)
	assert.Equal(t, int64(1520100060000000000), b.until)

	b.state = halfOpen
	b.now = now(1520100001)

	var wg sync.WaitGroup
	wg.Add(20)
	for i := 0; i < 20; i++ {
		go func() {
			err = b.Execute(func() error { return nil })
			assert.Nil(t, err)
			wg.Done()
		}()
	}

	wg.Wait()
	assert.True(t, b.total <= 10)
	assert.Equal(t, uint32(0), b.failures)
	assert.Equal(t, closed, b.state)
	assert.Equal(t, int64(1520100061000000000), b.until)
}

func now(sec int64) func() time.Time {
	return func() time.Time { return time.Unix(sec, 0) }
}
