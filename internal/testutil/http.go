package testutil

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
)

// Client is a browser-like API client: it keeps cookies and echoes the accessflow_csrf
// cookie in the X-CSRF-Token header, as the web app does.
type Client struct {
	T    testing.TB
	Srv  *httptest.Server
	HTTP *http.Client
	CSRF bool // send the CSRF header (default true)
}

func NewClient(t testing.TB, srv *httptest.Server) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{T: t, Srv: srv, HTTP: &http.Client{Jar: jar}, CSRF: true}
}

// Response is a response, decoded when it is JSON.
type Response struct {
	Status int
	Body   map[string]any
	Header http.Header
	Raw    []byte
}

func (c *Client) Do(method, path string, body any) Response {
	c.T.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case []byte: // a file, sent as is
		r = bytes.NewReader(b)
	default:
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, c.Srv.URL+path, r)
	if c.CSRF {
		if v := c.Cookie("accessflow_csrf"); v != "" {
			req.Header.Set("X-CSRF-Token", v)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		c.T.Fatal(err)
	}
	defer resp.Body.Close()
	out := Response{Status: resp.StatusCode, Header: resp.Header}
	out.Raw, _ = io.ReadAll(resp.Body)
	_ = json.Unmarshal(out.Raw, &out.Body)
	return out
}

func (c *Client) Cookie(name string) string {
	u, _ := url.Parse(c.Srv.URL)
	for _, ck := range c.HTTP.Jar.Cookies(u) {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

func (c *Client) SetCookie(name, value string) {
	u, _ := url.Parse(c.Srv.URL)
	c.HTTP.Jar.SetCookies(u, []*http.Cookie{{Name: name, Value: value, Path: "/"}})
}

func (c *Client) Login(email, password string) Response {
	c.T.Helper()
	return c.Do(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
}

// RequireStatus fails unless r has status and, when code is set, that error code.
func RequireStatus(t testing.TB, r Response, status int, code string) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("status = %d (body %v); want %d", r.Status, r.Body, status)
	}
	if code != "" && r.Body["error"] != code {
		t.Fatalf("error = %v; want %q", r.Body["error"], code)
	}
}
