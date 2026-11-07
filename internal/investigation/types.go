// Package investigation defines the versioned, transport-independent proposal core.
package investigation

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unicode"
)

const Version = 1

var (
	ErrConflict  = errors.New("conflict")
	ErrNotFound  = errors.New("not found")
	ErrInvalid   = errors.New("invalid argument")
	ErrRecording = errors.New("recording unavailable")
)

func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// Action has exactly one parameter set. Restart values are frozen by review,
// never accepted from an agent as executable bytes.
type Action struct {
	Kind      string `json:"kind"`
	Replicas  *int32 `json:"replicas,omitempty"`
	Container string `json:"container,omitempty"`
	Image     string `json:"image,omitempty"`
	Revision  int64  `json:"revision,omitempty"`
}

func (a Action) Validate() error {
	switch a.Kind {
	case "scale":
		if a.Replicas != nil && *a.Replicas >= 0 && a.Container == "" && a.Image == "" && a.Revision == 0 {
			return nil
		}
	case "image":
		if a.Replicas == nil && a.Container != "" && a.Image != "" && len(a.Image) <= 4096 && safeActionText(a.Image+a.Container) && a.Revision == 0 {
			return nil
		}
	case "restart":
		if a.Replicas == nil && a.Container == "" && a.Image == "" && a.Revision == 0 {
			return nil
		}
	case "rollback":
		if a.Replicas == nil && a.Container == "" && a.Image == "" && a.Revision > 0 {
			return nil
		}
	}
	return fmt.Errorf("%w: invalid action parameters", ErrInvalid)
}

// Executable identifiers must not change meaning when rendered in a terminal.
// Kubernetes accepts image strings that contain invisible format characters;
// silently stripping those in review would approve different visible bytes.
func safeActionText(value string) bool {
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return false
		}
	}
	return true
}

type Target struct {
	Cluster    string `json:"cluster"`
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
}

func (t Target) Validate(cluster string) error {
	if cluster == "" || t.Cluster != cluster || t.APIVersion != "apps/v1" || t.Kind != "Deployment" || t.Namespace == "" || t.Name == "" || t.UID == "" {
		return fmt.Errorf("%w: target must be an identified Deployment in the pinned cluster", ErrInvalid)
	}
	return nil
}

type Observed struct {
	ResourceVersion string    `json:"resourceVersion"`
	Generation      int64     `json:"generation"`
	Time            time.Time `json:"time"`
}

type Alternative struct {
	Target       Target   `json:"target"`
	Observed     Observed `json:"observed"`
	Action       Action   `json:"action"`
	Rationale    string   `json:"rationale"`
	Evidence     []string `json:"evidence,omitempty"`
	Verification string   `json:"verification"`
}

func (a Alternative) Validate(cluster string) error {
	if err := a.Target.Validate(cluster); err != nil {
		return err
	}
	if err := a.Action.Validate(); err != nil {
		return err
	}
	if a.Observed.ResourceVersion == "" || a.Observed.Time.IsZero() || a.Observed.Generation < 0 {
		return fmt.Errorf("%w: observed version and timestamp required", ErrInvalid)
	}

	return nil
}

type Proposal struct {
	ID          string      `json:"id"`
	Revision    int         `json:"revision"`
	Group       string      `json:"group"`
	Author      string      `json:"author"`
	State       string      `json:"state"`
	Alternative Alternative `json:"alternative"`
	// Agent observations are unverified until broker-owned preparation in M2.
	Verified bool `json:"verified"`
}

type Decision struct {
	Prepared  *Review   `json:"prepared,omitempty"`
	ID        string    `json:"id"`
	Proposal  string    `json:"proposal"`
	Revision  int       `json:"revision"`
	Review    string    `json:"review"`
	Digest    string    `json:"digest"`
	Operator  string    `json:"operator"`
	Time      time.Time `json:"time"`
	Choice    string    `json:"choice"`
	Overrides []string  `json:"overrides,omitempty"`
	Operation string    `json:"operation,omitempty"`
}

type Execution struct {
	ID              string    `json:"id"`
	Claim           string    `json:"claim,omitempty"`
	Decision        string    `json:"decision"`
	State           string    `json:"state"`
	Time            time.Time `json:"time"`
	UID             string    `json:"uid,omitempty"`
	ResourceVersion string    `json:"resourceVersion,omitempty"`
	Generation      int64     `json:"generation,omitempty"`
	Detail          string    `json:"detail,omitempty"`
}

type Investigation struct {
	Evidence   []Evidence  `json:"evidence,omitempty"`
	ID         string      `json:"id"`
	Session    string      `json:"session"`
	Cluster    string      `json:"cluster"`
	Context    string      `json:"context"`
	Target     *Target     `json:"target,omitempty"`
	Summary    string      `json:"summary"`
	Created    time.Time   `json:"created"`
	Proposals  []Proposal  `json:"proposals"`
	Decisions  []Decision  `json:"decisions"`
	Executions []Execution `json:"executions"`
}
