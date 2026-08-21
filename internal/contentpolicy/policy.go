package contentpolicy

import "context"

type Phase string

const (
	PreRequest     Phase = "PRE_REQUEST"
	ProviderNative Phase = "PROVIDER_NATIVE"
	PostResponse   Phase = "POST_RESPONSE"
)

type Request struct {
	Phase          Phase
	OrganizationID string
	UserID         string
	Model          string
	RequestID      string
	Body           []byte
	Response       []byte
}

type Decision struct {
	Allowed        bool
	Action         string
	Reason         string
	FailureMode    string
	ReviewRequired bool
}

type Provider interface {
	Evaluate(context.Context, Request) (Decision, error)
}

// NoopProvider is deliberately permissive. Deployments that enable content
// governance should inject a provider and choose its failure mode explicitly.
type NoopProvider struct{}

func (NoopProvider) Evaluate(context.Context, Request) (Decision, error) {
	return Decision{Allowed: true, Action: "ALLOW", FailureMode: "FAIL_OPEN"}, nil
}
