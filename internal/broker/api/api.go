// Package api is the investigation-only broker contract. There is intentionally
// no approval, override, execution or lifecycle method on InvestigationClient.
package api

import (
	"context"
	"encoding/json"
	"fmt"

	inv "github.com/YunusAndreasson/safina/internal/investigation"
)

const Version = 1

type Request struct {
	Version int             `json:"version"`
	ID      string          `json:"requestId"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type Response struct {
	Version int             `json:"version"`
	ID      string          `json:"requestId"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

type StartRequest struct {
	Session string      `json:"sessionId"`
	Key     string      `json:"idempotencyKey"`
	Context string      `json:"context"`
	Target  *inv.Target `json:"target,omitempty"`
	Summary string      `json:"summary"`
}

type SubmitRequest struct {
	Session       string            `json:"sessionId"`
	Key           string            `json:"idempotencyKey"`
	Investigation string            `json:"investigationId"`
	Alternatives  []inv.Alternative `json:"alternatives"`
}

type GetRequest struct {
	Investigation string `json:"investigationId"`
}

type View struct {
	History       string            `json:"history,omitempty"`
	Investigation inv.Investigation `json:"investigation"`
	Warnings      []string          `json:"warnings,omitempty"`
	Undelivered   int               `json:"undelivered"`
}

type Submitted struct {
	Group string `json:"groupId"`
}

type InvestigationClient interface {
	Contexts(context.Context, Empty) (Contexts, error)
	Inspect(context.Context, InspectRequest) (Observation, error)
	Events(context.Context, EventsRequest) (Observation, error)
	Logs(context.Context, LogsRequest) (Observation, error)
	History(context.Context, HistoryRequest) (HistoryResult, error)
	Evidence(context.Context, EvidenceRequest) (Observation, error)
	Start(context.Context, StartRequest) (View, error)
	Submit(context.Context, SubmitRequest) (Submitted, error)
	Get(context.Context, GetRequest) (View, error)
}
