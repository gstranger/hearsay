package main

import "testing"

func TestPickProviderKind_ManagedWinsWhenSet(t *testing.T) {
	if got := pickProviderKind("https://hearsay.example.com", false); got != ProviderManaged {
		t.Fatalf("expected ProviderManaged, got %v", got)
	}
}

func TestPickProviderKind_FallsBackToD1(t *testing.T) {
	if got := pickProviderKind("", true); got != ProviderD1 {
		t.Fatalf("expected ProviderD1, got %v", got)
	}
}

func TestPickProviderKind_ReturnsNoneWhenNeitherConfigured(t *testing.T) {
	if got := pickProviderKind("", false); got != ProviderNone {
		t.Fatalf("expected ProviderNone, got %v", got)
	}
}

func TestPickProviderKind_ManagedWinsEvenWhenD1AlsoSet(t *testing.T) {
	if got := pickProviderKind("https://hearsay.example.com", true); got != ProviderManaged {
		t.Fatalf("expected ProviderManaged (managed wins), got %v", got)
	}
}
