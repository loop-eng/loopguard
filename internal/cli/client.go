package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"github.com/loop-eng/loopguard/internal/api"
)

func daemonClient() *http.Client {
	sockPath := api.SocketPath()
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}
}

func fetchStatus() (*api.StatusResponse, error) {
	client := daemonClient()
	resp, err := client.Get("http://loopguard/api/status")
	if err != nil {
		return nil, fmt.Errorf("cannot connect to daemon: %w", err)
	}
	defer resp.Body.Close()

	var status api.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	return &status, nil
}

func resumeSession(id string) error {
	client := daemonClient()
	resp, err := client.Post("http://loopguard/api/sessions/"+url.PathEscape(id)+"/resume", "application/json", nil)
	if err != nil {
		return fmt.Errorf("cannot connect to daemon: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("%s", errResp["error"])
	}
	return nil
}
