// Package access holds grants, operations and fulfillment, and the client for the Mock Tool
// Service, reached only over HTTP (A1, A2).
package access

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

// ToolClient applies operations to a tool and looks up an account's actual state. It is
// the one interface A8 allows: tests inject faults behind it.
type ToolClient interface {
	Apply(ctx context.Context, op ToolOperation) (ToolResult, error)
	Account(ctx context.Context, tool, account string) (ToolAccount, error)
}

// ToolOperation grants, revokes, suspends or resumes one (tool, account, level) (D4).
// Tool and Level are AccessFlow IDs; Account is the grant's account: the employee's email,
// or the tool-specific account the request named (a GitHub login, A1 note 2026-11-01).
type ToolOperation struct {
	ID                         uuid.UUID
	Type, Tool, Account, Level string
}

// ExternalAccount is a tool-specific account (A1 note 2026-11-01): the name the tool knows
// the employee by, as the tool spells it, and its immutable reference (GitHub's user id).
type ExternalAccount struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
}

// Why an external account cannot be granted to: a malformed name, no such account, or an
// account that is not a person's (a GitHub organization or bot).
var (
	ErrAccountInvalid    = errors.New("invalid account name")
	ErrAccountNotFound   = errors.New("account not found")
	ErrAccountNotAllowed = errors.New("not a user account")
	// ErrRateLimited is the tool refusing more calls for now (GitHub's primary or secondary
	// rate limit): temporary, like an outage.
	ErrRateLimited = errors.New("rate limited")
)

// ToolResult says whether the tool changed anything: APPLIED or NO_CHANGE.
type ToolResult struct{ Result string }

// ToolAccount is an account's actual state in a tool.
type ToolAccount struct {
	Exists bool
	Levels []ToolLevel
}

// ToolLevel is one permission: ACTIVE or SUSPENDED.
type ToolLevel struct{ Level, Status string }

// StatusError is a tool answer other than 200, kept so callers can tell a rejection
// (4xx) from a transient failure (5xx).
type StatusError struct {
	Status int
	Body   string
}

func (e *StatusError) Error() string { return fmt.Sprintf("mock tool status %d: %s", e.Status, e.Body) }

const maxResponse = 1 << 20

// HTTPToolClient calls the Mock Tool Service. Anything but a 200 with a well-formed body
// that echoes the request is an error, never success (A2, A6).
type HTTPToolClient struct {
	base string
	http *http.Client
}

// NewHTTPToolClient returns a client for baseURL; production uses a 5-second timeout (A6).
func NewHTTPToolClient(baseURL string, timeout time.Duration) *HTTPToolClient {
	return &HTTPToolClient{base: baseURL, http: &http.Client{Timeout: timeout}}
}

// wire is the JSON shape of an operation, sent and echoed back.
type wire struct {
	OperationID uuid.UUID `json:"operationId"`
	Type        string    `json:"type"`
	Tool        string    `json:"tool"`
	Account     string    `json:"account"`
	Level       string    `json:"level"`
	Result      string    `json:"result,omitempty"`
}

func (c *HTTPToolClient) Apply(ctx context.Context, op ToolOperation) (ToolResult, error) {
	sent := wire{op.ID, op.Type, op.Tool, op.Account, op.Level, ""}
	body, err := json.Marshal(sent)
	if err != nil {
		return ToolResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/operations", bytes.NewReader(body))
	if err != nil {
		return ToolResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var got wire
	if err := c.do(req, &got); err != nil {
		return ToolResult{}, err
	}
	echoed := got
	echoed.Result = ""
	if echoed != sent {
		return ToolResult{}, fmt.Errorf("mock tool echoed another operation: %+v", got)
	}
	if got.Result != "APPLIED" && got.Result != "NO_CHANGE" {
		return ToolResult{}, fmt.Errorf("mock tool result %q", got.Result)
	}
	return ToolResult{Result: got.Result}, nil
}

func (c *HTTPToolClient) Account(ctx context.Context, tool, account string) (ToolAccount, error) {
	// Escaped: valid emails may contain / ? # %.
	u := c.base + "/v1/tools/" + url.PathEscape(tool) + "/accounts/" + url.PathEscape(account)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ToolAccount{}, err
	}
	var got struct {
		Tool    string `json:"tool"`
		Account string `json:"account"`
		Exists  bool   `json:"exists"`
		Levels  *[]struct {
			Level  string `json:"level"`
			Status string `json:"status"`
		} `json:"levels"`
	}
	if err := c.do(req, &got); err != nil {
		return ToolAccount{}, err
	}
	if got.Tool != tool || got.Account != account || got.Levels == nil {
		return ToolAccount{}, fmt.Errorf("mock tool answered for %s/%s", got.Tool, got.Account)
	}
	state := ToolAccount{Exists: got.Exists, Levels: []ToolLevel{}}
	for _, l := range *got.Levels {
		if l.Status != "ACTIVE" && l.Status != "SUSPENDED" {
			return ToolAccount{}, fmt.Errorf("mock tool level status %q", l.Status)
		}
		state.Levels = append(state.Levels, ToolLevel{l.Level, l.Status})
	}
	return state, nil
}

// ToolHolding is one account's permissions in a tool, as the baseline import reads them.
type ToolHolding struct {
	Account string
	Levels  []ToolLevel
}

// Accounts lists every account holding a permission in tool (D10 baseline import).
func (c *HTTPToolClient) Accounts(ctx context.Context, tool string) ([]ToolHolding, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/tools/"+url.PathEscape(tool)+"/accounts", nil)
	if err != nil {
		return nil, err
	}
	var got struct {
		Tool     string `json:"tool"`
		Accounts []struct {
			Tool    string `json:"tool"`
			Account string `json:"account"`
			Exists  bool   `json:"exists"`
			Levels  []struct {
				Level  string `json:"level"`
				Status string `json:"status"`
			} `json:"levels"`
		} `json:"accounts"`
	}
	if err := c.do(req, &got); err != nil {
		return nil, err
	}
	if got.Tool != tool {
		return nil, fmt.Errorf("mock tool listed %s", got.Tool)
	}
	var out []ToolHolding
	for _, a := range got.Accounts {
		h := ToolHolding{Account: a.Account}
		for _, l := range a.Levels {
			if l.Status != "ACTIVE" && l.Status != "SUSPENDED" {
				return nil, fmt.Errorf("mock tool level status %q", l.Status)
			}
			h.Levels = append(h.Levels, ToolLevel{l.Level, l.Status})
		}
		out = append(out, h)
	}
	return out, nil
}

// do sends req and decodes exactly one JSON value with known fields from a 200 answer.
func (c *HTTPToolClient) do(req *http.Request, v any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body := io.LimitReader(resp.Body, maxResponse)
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(body)
		return &StatusError{Status: resp.StatusCode, Body: string(raw)}
	}
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("mock tool response: %w", err)
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("mock tool response: trailing data")
	}
	return nil
}
