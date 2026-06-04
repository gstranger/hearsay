package main

// ProviderKind names which storage backend the WASM runtime should use.
// Defined without a build tag so the selection logic is testable on native.
type ProviderKind int

const (
	ProviderNone ProviderKind = iota
	ProviderManaged
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
