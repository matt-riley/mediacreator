// Package provider defines the interface for asynchronous media generation
// backends and implementations for fal.ai and kie.ai.
package provider

import (
	"context"
	"strings"
)

// Phase describes the state of a generation job.
type Phase string

const (
	PhasePending   Phase = "pending"
	PhaseCompleted Phase = "completed"
	PhaseFailed    Phase = "failed"
)

// Job identifies an in-flight (or finished) generation job.
type Job struct {
	ID        string // request_id (fal) or taskId (kie)
	Model     string // model/endpoint used to create the job
	StatusURL string // absolute status URL when the provider returned one
	ResultURL string // absolute result URL when the provider returned one
}

// Status is the result of a single status poll.
type Status struct {
	Phase  Phase
	Detail string // human readable description (e.g. "IN_QUEUE (position 3)")
	Raw    map[string]any
}

// Provider is an asynchronous media generation backend.
type Provider interface {
	Name() string
	// Submit creates a job. It does not wait for completion.
	Submit(ctx context.Context, model string, input map[string]any, webhook string) (*Job, error)
	// Status polls the current state of a job.
	Status(ctx context.Context, job *Job) (*Status, error)
	// Result fetches the final result payload of a completed job.
	Result(ctx context.Context, job *Job) (map[string]any, error)
	// List returns a page of the provider's model catalog.
	List(ctx context.Context, opts ListOptions) (*ModelList, error)
}

// Model describes a model in a provider's catalog.
type Model struct {
	ID         string `json:"id"`
	Title      string `json:"title,omitempty"`
	Category   string `json:"category,omitempty"`
	Vendor     string `json:"vendor,omitempty"` // first path segment of the id
	Deprecated bool   `json:"deprecated,omitempty"`
}

// ModelList is the result of a catalog listing.
type ModelList struct {
	Models     []Model
	Total      int    // total catalog size when the provider reports one, else -1
	HasMore    bool   // true when more pages are available
	NextCursor string // cursor for the next page, if any
	Source     string // where the catalog came from: "live", "fallback", ...
}

// ListOptions controls catalog listing.
type ListOptions struct {
	Limit      int
	Page       int    // 1-based page (fal encodes it as a pagination cursor)
	All        bool   // fetch every page (fal only)
	Search     string // free-text query (fal: server-side; kie: local filter)
	Category   string // category filter, e.g. text-to-video (fal only)
	Status     string // "active", "deprecated", or "" for all (fal only)
	EndpointID string // fetch one specific endpoint id, find mode (fal only)
	Cursor     string // raw pagination cursor (fal; overrides Page)
}

// vendorOf returns the first path segment of a model id (e.g. "openai" for
// "openai/gpt-image-2/edit").
func vendorOf(id string) string {
	if i := strings.IndexByte(id, '/'); i > 0 {
		return id[:i]
	}
	return ""
}
