package runner

import (
	"context"
	"testing"
)

func TestResolveBinary_PrefersConfiguredWhenHealthy(t *testing.T) {
	t.Parallel()
	r := NewFakeRunner().
		AddCall(nil, nil, 0, nil)

	got, usedFallback, err := ResolveBinary(context.Background(), r, "configured-bin", "fallback-bin")
	if err != nil {
		t.Fatalf("ResolveBinary: %v", err)
	}
	if got != "configured-bin" {
		t.Fatalf("expected configured-bin, got %s", got)
	}
	if usedFallback {
		t.Fatal("expected usedFallback=false")
	}
}

func TestResolveBinary_UsesFallbackWhenConfiguredFails(t *testing.T) {
	t.Parallel()
	r := NewFakeRunner().
		AddCall(nil, nil, 1, nil). // configured --version non-healthy
		AddCall(nil, nil, 0, nil)  // fallback healthy

	got, usedFallback, err := ResolveBinary(context.Background(), r, "configured-bin", "fallback-bin")
	if err != nil {
		t.Fatalf("ResolveBinary: %v", err)
	}
	if got != "fallback-bin" {
		t.Fatalf("expected fallback-bin, got %s", got)
	}
	if !usedFallback {
		t.Fatal("expected usedFallback=true")
	}
}

func TestResolveBinary_FailsWhenAllCandidatesFail(t *testing.T) {
	t.Parallel()
	r := NewFakeRunner().
		AddCall(nil, nil, 1, nil).
		AddCall(nil, nil, 1, nil)

	_, _, err := ResolveBinary(context.Background(), r, "configured-bin", "fallback-bin")
	if err == nil {
		t.Fatal("expected error when all candidates fail")
	}
}
