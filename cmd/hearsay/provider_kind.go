package main

// ProviderKind names which storage backend the WASM runtime should use.
// Defined without a build tag so the selection logic is testable on native.
type ProviderKind int

const (
	// ProviderNone signals no backend is configured; the caller should
	// respond with HTTP 500.
	ProviderNone ProviderKind = iota
	// ProviderManaged selects the remote HTTP provider (internal/managed),
	// driven by HEARSAY_MANAGED_URL.
	ProviderManaged
	// ProviderD1 selects the Cloudflare D1 provider (internal/d1), driven
	// by the Worker's HEARSAY_D1 binding.
	ProviderD1
)

// pickProviderKind picks the backend given which env inputs are populated.
// HEARSAY_MANAGED_URL wins when set; otherwise the D1 binding is used;
// otherwise ProviderNone (the caller should respond with HTTP 500).
func pickProviderKind(managedURL string, hasD1Binding bool) ProviderKind {
	if managedURL != "" {
		return ProviderManaged
	}
	if hasD1Binding {
		return ProviderD1
	}
	return ProviderNone
}
