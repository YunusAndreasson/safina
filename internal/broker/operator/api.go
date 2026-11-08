// Package operator defines the TUI-only broker contract. The investigation
// client and MCP adapter must never expose these methods as tools.
package operator

import inv "github.com/YunusAndreasson/safina/internal/investigation"

type Request struct {
	Action        *inv.Action `json:"action,omitempty"`
	Investigation string      `json:"investigation"`
	Proposal      string      `json:"proposal"`
	Revision      int         `json:"revision"`
	Review        string      `json:"review,omitempty"`
	Digest        string      `json:"digest,omitempty"`
	Overrides     []string    `json:"overrides,omitempty"`
	Replicas      *int32      `json:"replicas,omitempty"`
}

// Inbox contains lightweight listing entries; use the investigation getter for
// complete evidence, rationale, decisions and outcomes.
type Inbox struct {
	Investigations []inv.Investigation `json:"investigations"`
	Pending        int                 `json:"pending"`
	Warnings       []string            `json:"warnings"`
}
