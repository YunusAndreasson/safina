package investigation

import "time"

// Review is broker-computed. Approval journals the complete executable change,
// not just an opaque digest, so recovery never needs an agent's explanation.
type Review struct {
	Action              Action    `json:"action"`
	Patch               []byte    `json:"-"`
	MutationDigest      string    `json:"mutationDigest,omitempty"`
	TemplateDigest      string    `json:"templateDigest,omitempty"`
	RestartAt           string    `json:"restartAt,omitempty"`
	RollbackUID         string    `json:"rollbackUID,omitempty"`
	RollbackVersion     string    `json:"rollbackVersion,omitempty"`
	History             string    `json:"history,omitempty"`
	ID                  string    `json:"id"`
	Investigation       string    `json:"investigation"`
	Proposal            string    `json:"proposal"`
	Revision            int       `json:"revision"`
	Target              Target    `json:"target"`
	ResourceVersion     string    `json:"resourceVersion"`
	Generation          int64     `json:"generation"`
	Before              int32     `json:"before"`
	After               int32     `json:"after"`
	Diff                string    `json:"diff"`
	Warnings            []string  `json:"warnings"`
	RequiredOverrides   []string  `json:"requiredOverrides"`
	ControllerSignature string    `json:"controllerSignature"`
	Drift               bool      `json:"drift"`
	Noop                bool      `json:"noop"`
	Time                time.Time `json:"time"`
	Digest              string    `json:"digest"`
}
