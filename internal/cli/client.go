package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/term"

	"waves/internal/config"
)

// client calls the service API with the operator token.
type client struct {
	base  string
	token string
	tty   bool
	http  *http.Client
}

// apiError is the contract error body.
type apiError struct {
	Status       int
	Code         string `json:"code"`
	Msg          string `json:"message"`
	Next         string `json:"next_step"`
	AttemptsLeft *int   `json:"attempts_left"`
}

func (e *apiError) Error() string {
	s := e.Msg
	if e.AttemptsLeft != nil {
		s += fmt.Sprintf(" %d attempts left.", *e.AttemptsLeft)
	}
	if e.Next != "" {
		s += " " + e.Next
	}
	return s
}

func (e *Env) client() (*client, error) {
	cfg, err := config.Load(e.Dirs)
	if err != nil {
		return nil, err
	}
	tok, err := config.ReadSecret(e.Dirs.OperatorToken())
	if err != nil {
		return nil, fmt.Errorf("no operator token; run waves setup first (%w)", err)
	}
	return &client{base: "http://" + cfg.Addr(), token: tok, tty: term.IsTerminal(int(e.In.Fd())), http: &http.Client{Timeout: 30 * time.Second}}, nil
}

func (c *client) do(ctx context.Context, method, path string, body any, out any, hdr map[string]string) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+"/api/v1"+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.tty {
		req.Header.Set("X-Waves-TTY", "1")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("the Waves service is not running at %s; start it with: waves start", c.base)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 400 {
		var eb struct {
			Error apiError `json:"error"`
		}
		if json.Unmarshal(b, &eb) != nil || eb.Error.Msg == "" {
			return fmt.Errorf("the service answered %d", res.StatusCode)
		}
		eb.Error.Status = res.StatusCode
		return &eb.Error
	}
	if out == nil {
		return nil
	}
	if s, ok := out.(*string); ok {
		*s = string(b)
		return nil
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}

func (c *client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, "GET", path, nil, out, nil)
}

func (c *client) post(ctx context.Context, path string, body, out any) error {
	if body == nil {
		body = map[string]any{}
	}
	return c.do(ctx, "POST", path, body, out, nil)
}

// healthy reports whether a service answers on the configured address.
func (e *Env) healthy(ctx context.Context) bool {
	cfg, err := config.Load(e.Dirs)
	if err != nil {
		return false
	}
	c, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(c, "GET", "http://"+cfg.Addr()+"/api/v1/health", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == 200
}

func (e *Env) printJSON(v any) error {
	enc := json.NewEncoder(e.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func wantJSON(args []string) ([]string, bool) {
	var out []string
	j := false
	for _, a := range args {
		if a == "--json" {
			j = true
			continue
		}
		out = append(out, a)
	}
	return out, j
}

// httpClientNoTimeout is for the event stream, which stays open.
var httpClientNoTimeout = http.Client{}

func newStreamRequest(ctx context.Context, c *client, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/api/v1"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "text/event-stream")
	return req, nil
}
