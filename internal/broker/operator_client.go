package broker

import (
	"context"

	"github.com/YunusAndreasson/safina/internal/broker/api"
	"github.com/YunusAndreasson/safina/internal/broker/operator"
	inv "github.com/YunusAndreasson/safina/internal/investigation"
)

// OperatorClient is deliberately separate from Client/InvestigationClient.
// Same-user arbitrary code is outside the local workflow trust boundary.
type OperatorClient struct{ client *Client }

func NewOperatorClient(o Options) *OperatorClient { return &OperatorClient{client: NewClient(o)} }
func (c *OperatorClient) Inbox(ctx context.Context) (operator.Inbox, error) {
	var out operator.Inbox
	err := c.client.callPath(ctx, "/v1/operator", "inbox", struct{}{}, &out)
	return out, err
}

func (c *OperatorClient) Prepare(ctx context.Context, p operator.Request) (inv.Review, error) {
	var out inv.Review
	err := c.client.callPath(ctx, "/v1/operator", "prepare", p, &out)
	return out, err
}

func (c *OperatorClient) Approve(ctx context.Context, p operator.Request) (inv.Execution, error) {
	var out inv.Execution
	err := c.client.callPath(ctx, "/v1/operator", "approve", p, &out)
	return out, err
}

func (c *OperatorClient) Revise(ctx context.Context, p operator.Request) (inv.Proposal, error) {
	var out inv.Proposal
	err := c.client.callPath(ctx, "/v1/operator", "revise", p, &out)
	return out, err
}

func (c *OperatorClient) Reject(ctx context.Context, p operator.Request) error {
	var out struct{}
	return c.client.callPath(ctx, "/v1/operator", "reject", p, &out)
}

// Investigation retrieves full selected detail; Inbox intentionally omits it.
func (c *OperatorClient) Investigation(ctx context.Context, id string) (inv.Investigation, error) {
	view, err := c.client.Get(ctx, api.GetRequest{Investigation: id})
	return view.Investigation, err
}
