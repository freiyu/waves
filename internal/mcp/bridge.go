package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// Bridge serves MCP on stdin and stdout for Claude Code and forwards every
// call to the service's /mcp endpoint with the project credential. The model
// never sees the credential; it lives in a 0600 file the bridge reads.
func Bridge(ctx context.Context, endpoint, token, version string, t sdk.Transport) error {
	client := sdk.NewClient(&sdk.Implementation{Name: "waves-bridge", Version: version}, nil)
	httpc := &http.Client{Transport: bearer{token, http.DefaultTransport}, Timeout: 0}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cs, err := client.Connect(cctx, &sdk.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpc}, nil)
	if err != nil {
		return fmt.Errorf("connect to the Waves service at %s: %w (is it running? waves status)", endpoint, err)
	}
	defer cs.Close()
	base := ctx
	srv := sdk.NewServer(&sdk.Implementation{Name: "waves", Version: version}, nil)
	n := 0
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return fmt.Errorf("list tools: %w", err)
		}
		name := tool.Name
		srv.AddTool(tool, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			args := json.RawMessage(req.Params.Arguments)
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			p := &sdk.CallToolParams{Name: name, Arguments: args}
			// Forward only Claude Code's own keys: the tool_use_id joins this call
			// with the guard hook event. Protocol keys of the stdio session stay here.
			fwd := map[string]any{}
			for k, v := range req.Params.GetMeta() {
				if strings.HasPrefix(k, "claudecode/") {
					fwd[k] = v
				}
			}
			if len(fwd) > 0 {
				p.SetMeta(fwd)
			}
			// Forward on the bridge's own context: the inbound request context
			// carries the stdio session's protocol details, which must not leak
			// into the HTTP session. Cancellation still follows the inbound call.
			fctx, cancel := context.WithCancel(base)
			defer cancel()
			stop := context.AfterFunc(ctx, cancel)
			defer stop()
			return cs.CallTool(fctx, p)
		})
		n++
	}
	if n == 0 {
		return fmt.Errorf("the service listed no tools")
	}
	return srv.Run(ctx, t)
}
