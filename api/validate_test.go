package api

import (
	"sync"
	"testing"
)

func TestValidationStatusString(t *testing.T) {
	for status, want := range map[validationStatus]string{
		validationNotStarted: "not_started",
		validationInProgress: "in_progress",
		validationSuccess:    "success",
		validationFailure:    "failure",
	} {
		if got := status.String(); got != want {
			t.Errorf("validationStatus(%d).String() = %q, want %q", int(status), got, want)
		}
	}
}

func newFeeValidationServer() *Server {
	return &Server{
		feeValidation: &feeValidation{
			status: validationNotStarted,
		},
	}
}

func TestFeeValidationRetrying(t *testing.T) {
	s := newFeeValidationServer()
	s.setFeeValidationRetrying("attempt %d failed", 1)
	v := s.getFeeValidationStatus()
	if v.status != validationInProgress {
		t.Fatalf("status = %s, want in_progress", v.status)
	}
	if v.err != "attempt 1 failed" {
		t.Fatalf("err = %q, want %q", v.err, "attempt 1 failed")
	}
}

func TestFeeValidationSuccess(t *testing.T) {
	s := newFeeValidationServer()
	onchain := []string{"912d5440f7e3769e"}
	s.setFeeValidationSuccess(onchain)
	v := s.getFeeValidationStatus()
	if v.status != validationSuccess {
		t.Fatalf("status = %s, want success", v.status)
	}
	if len(v.onchain) != 1 || v.onchain[0] != onchain[0] {
		t.Fatalf("onchain = %v, want %v", v.onchain, onchain)
	}
}

func TestFeeValidationFailure(t *testing.T) {
	s := newFeeValidationServer()
	missing := []string{"e1ac6b2740d204c2"}
	s.setFeeValidationFailure([]string{"912d5440f7e3769e", "e1ac6b2740d204c2"}, missing)
	v := s.getFeeValidationStatus()
	if v.status != validationFailure {
		t.Fatalf("status = %s, want failure", v.status)
	}
	if len(v.missing) != 1 || v.missing[0] != missing[0] {
		t.Fatalf("missing = %v, want %v", v.missing, missing)
	}
	if v.err == "" {
		t.Fatal("err is empty, want mismatch description")
	}
}

// TestFeeValidationRetryingKeepsDefinitiveResult checks that a transient
// error during a periodic re-check does not overwrite the last definitive
// result.
func TestFeeValidationRetryingKeepsDefinitiveResult(t *testing.T) {
	s := newFeeValidationServer()

	s.setFeeValidationSuccess([]string{"912d5440f7e3769e"})
	s.setFeeValidationRetrying("access node unavailable")
	v := s.getFeeValidationStatus()
	if v.status != validationSuccess {
		t.Fatalf("status = %s, want success to be preserved", v.status)
	}

	s.setFeeValidationFailure([]string{"912d5440f7e3769e"}, []string{"912d5440f7e3769e"})
	s.setFeeValidationRetrying("access node unavailable")
	v = s.getFeeValidationStatus()
	if v.status != validationFailure {
		t.Fatalf("status = %s, want failure to be preserved", v.status)
	}
}

// TestFeeValidationConcurrentAccess exercises the fee validation state from
// multiple goroutines so the race detector can verify the locking.
func TestFeeValidationConcurrentAccess(t *testing.T) {
	s := newFeeValidationServer()
	wg := sync.WaitGroup{}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.setFeeValidationRetrying("attempt failed")
				s.setFeeValidationSuccess([]string{"912d5440f7e3769e"})
				s.setFeeValidationFailure(
					[]string{"e1ac6b2740d204c2"},
					[]string{"e1ac6b2740d204c2"},
				)
				v := s.getFeeValidationStatus()
				_ = v.status.String()
				_ = len(v.onchain)
				_ = len(v.missing)
			}
		}()
	}
	wg.Wait()
}

// TestFeeValidationFailureRecovery checks that a later successful check
// replaces a previous mismatch, e.g. after the on-chain receiver list
// changes.
func TestFeeValidationFailureRecovery(t *testing.T) {
	s := newFeeValidationServer()
	s.setFeeValidationFailure([]string{"912d5440f7e3769e"}, []string{"912d5440f7e3769e"})
	s.setFeeValidationSuccess([]string{"912d5440f7e3769e"})
	v := s.getFeeValidationStatus()
	if v.status != validationSuccess {
		t.Fatalf("status = %s, want success", v.status)
	}
	if v.err != "" || len(v.missing) != 0 {
		t.Fatalf("err = %q, missing = %v, want both cleared", v.err, v.missing)
	}
}
