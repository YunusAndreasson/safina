package api

import (
	"time"

	"github.com/yunus/safina/internal/history"
	inv "github.com/yunus/safina/internal/investigation"
)

type (
	Empty    struct{}
	Contexts struct {
		Contexts []string  `json:"contexts"`
		Observed time.Time `json:"observed"`
		Warnings []string  `json:"warnings"`
	}
)

type InspectRequest struct {
	Investigation string `json:"investigationId"`
	Namespace     string `json:"namespace"`
	Name          string `json:"name"`
}
type EventsRequest struct {
	Investigation string `json:"investigationId"`
	Namespace     string `json:"namespace"`
	UID           string `json:"uid"`
}
type LogsRequest struct {
	Investigation string `json:"investigationId"`
	Namespace     string `json:"namespace"`
	Pod           string `json:"pod"`
	Container     string `json:"container"`
	Previous      bool   `json:"previous"`
}
type HistoryRequest struct {
	Investigation string    `json:"investigationId"`
	Namespace     string    `json:"namespace,omitempty"`
	Resource      string    `json:"resource,omitempty"`
	Since         time.Time `json:"since,omitempty"`
	Until         time.Time `json:"until,omitempty"`
	Limit         int       `json:"limit,omitempty"`
}
type EvidenceRequest struct {
	Investigation string `json:"investigationId"`
	Evidence      string `json:"evidenceId"`
}
type HistoryResult struct {
	Cluster   string           `json:"cluster"`
	Observed  time.Time        `json:"observed"`
	Records   []history.Record `json:"records"`
	Warnings  []string         `json:"warnings"`
	Truncated bool             `json:"truncated"`
}
type Observation struct {
	Evidence inv.Evidence `json:"evidence"`
}
