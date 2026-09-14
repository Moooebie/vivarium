// Package proxy hosts the Vivarium host-bridge network isolation proxy.
//
// DEFERRED: The dynamic TLS interception proxy (CA generation, SNI-based leaf
// minting, DNS hijack, dummy-token validation, and rate limiting) described in
// BACKEND.md §2.2 is not implemented in this milestone. The Bridge interface is
// provided so the daemon can wire the real implementation in later without
// restructuring. Noop is the placeholder used today.
package proxy

import "context"

// Bridge is the host-bridge proxy contract.
type Bridge interface {
	// Start begins serving on the bridge gateway.
	Start(ctx context.Context) error
	// Close stops the proxy and releases listeners.
	Close() error
}

// Noop is a Bridge that does nothing.
type Noop struct{}

// Start implements Bridge.
func (Noop) Start(context.Context) error { return nil }

// Close implements Bridge.
func (Noop) Close() error { return nil }
