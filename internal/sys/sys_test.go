package sys

import (
	"strings"
	"testing"
	"time"
)

func TestExecRuns(t *testing.T) {
	out, err := Exec{}.Run("echo", "hi")
	if err != nil || out != "hi\n" {
		t.Fatalf("%q %v", out, err)
	}
}

// A command that never returns must be killed at the limit, not wait for its own end.
func TestExecTimesOut(t *testing.T) {
	start := time.Now()
	_, err := Exec{Timeout: 150 * time.Millisecond}.Run("sleep", "30")
	if err == nil || !strings.Contains(err.Error(), "timed out after 150ms") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %s", d)
	}
}

// A child that inherits the pipes and outlives the kill must not hold Run (WaitDelay).
func TestExecTimeoutWithAChildHoldingThePipe(t *testing.T) {
	start := time.Now()
	_, err := Exec{Timeout: 150 * time.Millisecond}.Run("sh", "-c", "sleep 3 & wait")
	if err == nil {
		t.Fatal("expected an error")
	}
	if d := time.Since(start); d > 2500*time.Millisecond {
		t.Fatalf("took %s", d)
	}
}

func TestExecDefaultTimeoutIsFifteenSeconds(t *testing.T) {
	if DefaultTimeout != 15*time.Second {
		t.Fatal(DefaultTimeout)
	}
}
