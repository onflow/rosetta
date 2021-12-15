// Public Domain (-) 2010-present, The Web4 Authors.
// See the Web4 UNLICENSE file for details.

// Package process provides utilities for managing the current system process.
package process

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var (
	exiting  bool
	mu       sync.RWMutex // protects exiting, registry
	registry = map[os.Signal][]func(){}
)

// Exit runs the registered exit handlers, as if the os.Interrupt signal had
// been sent, and then terminates the process with the given status code. Exit
// blocks until the process terminates if it has already been called elsewhere.
func Exit(code int) {
	mu.Lock()
	if exiting {
		mu.Unlock()
		return
	}
	exiting = true
	handlers := clone(registry[os.Interrupt])
	mu.Unlock()
	for _, handler := range handlers {
		handler()
	}
	os.Exit(code)
}

// SetExitHandler registers the given handler function to run when receiving
// os.Interrupt or SIGTERM signals. Registered handlers are executed in reverse
// order of when they were set.
func SetExitHandler(handler func()) {
	mu.Lock()
	registry[os.Interrupt] = prepend(registry[os.Interrupt], handler)
	registry[syscall.SIGTERM] = prepend(registry[syscall.SIGTERM], handler)
	mu.Unlock()
}

// SetSignalHandler registers the given handler function to run when receiving
// the specified signal. Registered handlers are executed in reverse order of
// when they were set.
func SetSignalHandler(signal os.Signal, handler func()) {
	mu.Lock()
	registry[signal] = prepend(registry[signal], handler)
	mu.Unlock()
}

func clone(xs []func()) []func() {
	ys := make([]func(), len(xs))
	copy(ys, xs)
	return ys
}

func handleSignals() {
	notifier := make(chan os.Signal, 100)
	signal.Notify(notifier)
	go func() {
		for sig := range notifier {
			mu.Lock()
			if sig == syscall.SIGTERM || sig == os.Interrupt {
				exiting = true
			}
			handlers := clone(registry[sig])
			mu.Unlock()
			for _, handler := range handlers {
				handler()
			}
			if sig == syscall.SIGTERM || sig == os.Interrupt {
				os.Exit(1)
			}
		}
	}()
}

func prepend(xs []func(), handler func()) []func() {
	return append([]func(){handler}, xs...)
}

func init() {
	handleSignals()
}
