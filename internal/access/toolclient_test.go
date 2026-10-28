package access_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/mocktools"
	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// timeout is the short client timeout of the fault cases; the others use normal, the
// production value, so a loaded machine cannot fail them.
const (
	timeout = 300 * time.Millisecond
	normal  = 5 * time.Second
)

// Interface check: the HTTP client is what the runner (M6) will use.
var _ access.ToolClient = (*access.HTTPToolClient)(nil)

// newMock runs the real Mock Tool Service handler on a real mock database behind Faults.
func newMock(t *testing.T, timeout time.Duration) (*access.HTTPToolClient, *testutil.Faults, *httptest.Server) {
	t.Helper()
	faults := testutil.NewFaults(mocktools.Handler(testutil.NewMockToolsDB(t, mocktools.Migrations)))
	srv := httptest.NewServer(faults)
	t.Cleanup(srv.Close)
	return access.NewHTTPToolClient(srv.URL, timeout), faults, srv
}

func grant(account string) access.ToolOperation {
	return access.ToolOperation{ID: uuid.New(), Type: "GRANT", Tool: uuid.NewString(), Account: account, Level: uuid.NewString()}
}

func TestApplyAndLookup(t *testing.T) {
	client, _, _ := newMock(t, normal)
	ctx := context.Background()
	for _, account := range []string{"ada@example.com", "a/b?c#d%e@x.com"} {
		op := grant(account)
		res, err := client.Apply(ctx, op)
		if err != nil || res.Result != "APPLIED" {
			t.Fatalf("apply %s: %v %v", account, res, err)
		}
		state, err := client.Account(ctx, op.Tool, account)
		if err != nil || !state.Exists || len(state.Levels) != 1 || state.Levels[0] != (access.ToolLevel{Level: op.Level, Status: "ACTIVE"}) {
			t.Fatalf("lookup %s: %+v %v", account, state, err)
		}
	}
}

func TestLookupOfAnUnknownAccount(t *testing.T) {
	client, _, _ := newMock(t, normal)
	state, err := client.Account(context.Background(), "tool", "nobody@example.com")
	if err != nil || state.Exists || len(state.Levels) != 0 {
		t.Fatalf("%+v %v", state, err)
	}
}

// stub answers every request with status and body.
func stub(t *testing.T, status int, body string) *access.HTTPToolClient {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return access.NewHTTPToolClient(srv.URL, normal)
}

func TestClientTreatsFaultsAsErrors(t *testing.T) {
	ctx := context.Background()
	op := grant("ada@example.com")
	echo := `{"operationId":"` + op.ID.String() + `","type":"GRANT","tool":"` + op.Tool + `","account":"ada@example.com","level":"` + op.Level + `"`
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	for _, tc := range []struct {
		name   string
		client func(t *testing.T) *access.HTTPToolClient
		status int // the StatusError's status, when the tool answered with one
	}{
		{"500", func(t *testing.T) *access.HTTPToolClient { c, f, _ := newMock(t, normal); f.FailNext(1, 500); return c }, 500},
		{"503", func(t *testing.T) *access.HTTPToolClient { c, f, _ := newMock(t, normal); f.FailNext(1, 503); return c }, 503},
		{"409 from the tool", func(t *testing.T) *access.HTTPToolClient { return stub(t, 409, `{"error":"operation_id_reused"}`) }, 409},
		{"timeout", func(t *testing.T) *access.HTTPToolClient {
			c, f, _ := newMock(t, timeout)
			f.DelayNext(2 * timeout)
			return c
		}, 0},
		{"malformed body", func(t *testing.T) *access.HTTPToolClient { c, f, _ := newMock(t, normal); f.MalformNext(); return c }, 0},
		{"another operation echoed", func(t *testing.T) *access.HTTPToolClient {
			return stub(t, 200, `{"operationId":"`+uuid.NewString()+`","type":"GRANT","tool":"`+op.Tool+`","account":"ada@example.com","level":"`+op.Level+`","result":"APPLIED"}`)
		}, 0},
		{"unknown result", func(t *testing.T) *access.HTTPToolClient { return stub(t, 200, echo+`,"result":"MAYBE"}`) }, 0},
		{"unknown field", func(t *testing.T) *access.HTTPToolClient { return stub(t, 200, echo+`,"result":"APPLIED","extra":1}`) }, 0},
		{"trailing data", func(t *testing.T) *access.HTTPToolClient { return stub(t, 200, echo+`,"result":"APPLIED"} {}`) }, 0},
		{"201 instead of 200", func(t *testing.T) *access.HTTPToolClient { return stub(t, 201, echo+`,"result":"APPLIED"}`) }, 201},
		{"closed server", func(t *testing.T) *access.HTTPToolClient { return access.NewHTTPToolClient(closed.URL, normal) }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := tc.client(t)
			start := time.Now()
			res, err := client.Apply(ctx, op)
			if err == nil {
				t.Fatalf("Apply = %+v, nil; want an error", res)
			}
			if elapsed := time.Since(start); tc.name == "timeout" && elapsed > timeout+500*time.Millisecond {
				t.Fatalf("took %v; the timeout is %v", elapsed, timeout)
			}
			var se *access.StatusError
			if got := errors.As(err, &se); got != (tc.status != 0) || (got && se.Status != tc.status) {
				t.Fatalf("error %v; want StatusError %d: %v", err, tc.status, got)
			}
		})
	}
}

func TestLookupTreatsFaultsAsErrors(t *testing.T) {
	ctx := context.Background()
	for name, client := range map[string]*access.HTTPToolClient{
		"500":             stub(t, 500, ``),
		"404":             stub(t, 404, `{"error":"not_found"}`),
		"another account": stub(t, 200, `{"tool":"t","account":"bob@example.com","exists":false,"levels":[]}`),
		"unknown status":  stub(t, 200, `{"tool":"t","account":"ada@example.com","exists":true,"levels":[{"level":"l","status":"GONE"}]}`),
		"missing levels":  stub(t, 200, `{"tool":"t","account":"ada@example.com","exists":true}`),
		"malformed":       stub(t, 200, `{"tool":`),
	} {
		t.Run(name, func(t *testing.T) {
			if state, err := client.Account(ctx, "t", "ada@example.com"); err == nil {
				t.Fatalf("Account = %+v, nil; want an error", state)
			}
		})
	}
}

// The tool applied the grant but the response was lost: the retry with the same operation
// ID succeeds and the account holds the level once.
func TestRetryAfterALostResponse(t *testing.T) {
	client, faults, _ := newMock(t, normal)
	ctx := context.Background()
	op := grant("ada@example.com")
	faults.ApplyThenFail()
	if _, err := client.Apply(ctx, op); err == nil {
		t.Fatal("the lost response must be an error")
	}
	res, err := client.Apply(ctx, op)
	if err != nil || res.Result != "APPLIED" {
		t.Fatalf("retry: %+v %v; want the stored APPLIED", res, err)
	}
	state, _ := client.Account(ctx, op.Tool, op.Account)
	if len(state.Levels) != 1 || state.Levels[0].Status != "ACTIVE" {
		t.Fatalf("levels %+v", state.Levels)
	}
}
