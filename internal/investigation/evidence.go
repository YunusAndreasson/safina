package investigation

import "time"

type Evidence struct {
	ID        string    `json:"id"`
	Cluster   string    `json:"cluster"`
	Namespace string    `json:"namespace,omitempty"`
	Resource  string    `json:"resource,omitempty"`
	Kind      string    `json:"kind"`
	Observed  time.Time `json:"observed"`
	Text      string    `json:"text"`
	Truncated bool      `json:"truncated"`
	Warnings  []string  `json:"warnings,omitempty"`
}
