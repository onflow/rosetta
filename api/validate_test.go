package api

import (
	"errors"
	"sync"
	"testing"

	"github.com/onflow/rosetta/config"
	"github.com/onflow/rosetta/indexdb"
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

func TestIsMissingFeeReceiverFunc(t *testing.T) {
	for name, tt := range map[string]struct {
		err  error
		want bool
	}{
		"missing member": {
			err:  errors.New("rpc error: code = InvalidArgument desc = failed to execute script: error: value of type `&FlowFees` has no member `getFeeReceiverAddresses`"),
			want: true,
		},
		"unavailable access node": {
			err:  errors.New("rpc error: code = Unavailable desc = connection refused"),
			want: false,
		},
		"unrelated member error": {
			err:  errors.New("error: value of type `&FlowToken` has no member `getBalance`"),
			want: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := isMissingFeeReceiverFunc(tt.err); got != tt.want {
				t.Errorf("isMissingFeeReceiverFunc(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestFeeValidationFallback(t *testing.T) {
	s := newFeeValidationServer()
	s.Chain = &config.Chain{
		Contracts: &config.Contracts{FlowFees: "912d5440f7e3769e"},
	}
	s.setFeeValidationFallback()
	v := s.getFeeValidationStatus()
	if v.status != validationSuccess {
		t.Fatalf("status = %s, want success", v.status)
	}
	if len(v.onchain) != 1 || v.onchain[0] != "912d5440f7e3769e" {
		t.Fatalf("onchain = %v, want [912d5440f7e3769e]", v.onchain)
	}
}

func TestCurrentFeeAddrs(t *testing.T) {
	store := indexdb.New(t.TempDir())
	chain := &config.Chain{
		Contracts: &config.Contracts{
			FlowFees:     "912d5440f7e3769e",
			FeeReceivers: []string{"e1ac6b2740d204c2"},
		},
	}
	s := &Server{
		Chain:    chain,
		Index:    store,
		feeAddrs: chain.Contracts.FeeAddresses(),
	}
	flowFees := []byte{0x91, 0x2d, 0x54, 0x40, 0xf7, 0xe3, 0x76, 0x9e}
	configured := []byte{0xe1, 0xac, 0x6b, 0x27, 0x40, 0xd2, 0x04, 0xc2}
	child := []byte{0x05, 0xcb, 0xd2, 0xfa, 0x51, 0x28, 0x04, 0x1d}

	// Without any indexed event, the configured fee addresses apply.
	addrs := s.currentFeeAddrs(100)
	if !addrs[string(flowFees)] || !addrs[string(configured)] || addrs[string(child)] {
		t.Fatalf("currentFeeAddrs without event = %v, want the configured fee addresses", addrs)
	}

	// An indexed event overrides the configured fee addresses.
	if err := store.SetFeeReceivers(50, [][]byte{child}); err != nil {
		t.Fatalf("SetFeeReceivers: %s", err)
	}
	addrs = s.currentFeeAddrs(100)
	if !addrs[string(flowFees)] || !addrs[string(child)] || addrs[string(configured)] {
		t.Fatalf("currentFeeAddrs with event = %v, want the FlowFees account and the event's child account", addrs)
	}

	// Events after the given height do not apply.
	addrs = s.currentFeeAddrs(49)
	if !addrs[string(configured)] || addrs[string(child)] {
		t.Fatalf("currentFeeAddrs before the event = %v, want the configured fee addresses", addrs)
	}
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
