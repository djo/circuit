circuit
=======

Implementation of [the circuit breaker](https://docs.microsoft.com/en-us/azure/architecture/patterns/circuit-breaker) pattern in Go.

The circuit breaker can prevent an application from repeatedly trying to execute an operation that's likely to fail.
It is implemented as a state machine with the following states:

- `Closed`: the request from the application is allowed to pass.
- `Half-Open`: a limited number of requests are allowed to pass.
- `Open`: the request is failed immediately and an error returned.

Usage
-----

```go
func NewBreaker(interval time.Duration, cooldown time.Duration, atLeastReqs uint32, toOpen ToState, toClosed ToState) (*Breaker, error)
```

- `interval` is the cyclic period of the closed state.
- `cooldown` is the period of the open state,
- `atLeastReqs` is the number of requests to consider in the half-open state
  before invoking a given toClosed function for decision making.
- `toOpen` is called whenever a request fails in the closed state.
  If it returns true, the circuit breaker will be placed into the open state.
- `toClosed` is called in the half-open state once the number of requests reached atLeastReqs.
  If it returns true, the circuit breaker will be placed into the closed state,
  otherwise into the open state.

A function signature of `toOpen` and `toClosed`:

```go
func(total uint32, failures uint32) bool
```

`Execute` runs a given request if the circuit breaker accepts it,
cases when it's in the closed state, or half-open one
and the number of requests has not yet reached `atLeastReqs`.

Returns ErrBreakerOpen when it doesn't accept the request,
otherwise the error from the req function:

```go
func (b *Breaker) Execute(req func() error) error
```

Example
-------

```go
// open the circuit breaker in case of 5% of failed requests
toOpen := func(total uint32, failures uint32) bool {
	return total > 0 && float64(failures)/float64(total) >= 0.05
}

// close the circuit breaker only if no failures
toClosed := func(total uint32, failures uint32) bool {
	return failures == 0
}

b, err := circuit.NewBreaker(time.Minute, 10*time.Second, 1, toOpen, toClosed)
if err != nil {
	panic(err)
}

func GetStatus(url string) (string, error) {
	var resp *http.Response
	err = b.Execute(func() error {
		resp, err = http.Get(url)
		return err
	})

	if err == circuit.ErrBreakerOpen {
		// the circuit breaker failed fast,
		// there is still time for fallback
		return "200 (cache)", nil
	}

	if err != nil {
		// the request failed
		return "", err
	}

	return resp.Status, nil
}
```
