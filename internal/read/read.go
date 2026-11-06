// Package read makes the read-only AxonOps API calls that give the design
// stage real dashboard and chart names. It never writes.
package read

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Config locates and authenticates against an AxonOps server.
type Config struct {
	BaseURL   string // empty for AxonOps SaaS
	Org       string
	Token     string
	TokenType string // "Bearer" (default, SaaS) or "AxonApi" (self-hosted)
	HTTP      *http.Client
}

// Context is what the design stage learns about a cluster.
type Context struct {
	Dashboards []Dashboard `json:"dashboards"`
}

// Dashboard is a dashboard template and its chart titles.
type Dashboard struct {
	Name   string   `json:"name"`
	Charts []string `json:"charts"`
}

// Client reads cluster context.
type Client struct{ cfg Config }

// New returns a Client; it validates the config.
func New(cfg Config) (*Client, error) {
	if cfg.Org == "" {
		return nil, fmt.Errorf("read: org is required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("read: set AXONOPS_API_TOKEN")
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.TokenType == "" {
		cfg.TokenType = "Bearer"
	}
	return &Client{cfg: cfg}, nil
}

func (c *Client) root() string {
	if c.cfg.BaseURL != "" {
		return strings.TrimRight(c.cfg.BaseURL, "/")
	}
	return "https://dash.axonops.cloud/" + url.PathEscape(c.cfg.Org)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.root()+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.cfg.TokenType+" "+c.cfg.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		hint := ""
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			hint = fmt.Sprintf(" (%s auth at %s; self-hosted needs AXONOPS_BASE_URL and AXONOPS_TOKEN_TYPE=AxonApi)", c.cfg.TokenType, c.root())
		}
		return fmt.Errorf("GET %s: HTTP %d%s", path, resp.StatusCode, hint)
	}
	return json.Unmarshal(body, out)
}

// Cluster reads the dashboards and charts of a cluster.
func (c *Client) Cluster(ctx context.Context, clusterType, cluster string) (Context, error) {
	var tpl struct {
		Dashboards []struct {
			Name   string `json:"name"`
			Panels []struct {
				Title string `json:"title"`
			} `json:"panels"`
		} `json:"dashboards"`
	}
	path := fmt.Sprintf("/api/v1/dashboardtemplate/%s/%s/%s",
		url.PathEscape(c.cfg.Org), url.PathEscape(clusterType), url.PathEscape(cluster))
	if err := c.get(ctx, path, &tpl); err != nil {
		return Context{}, err
	}
	var out Context
	for _, d := range tpl.Dashboards {
		dash := Dashboard{Name: d.Name}
		seen := map[string]bool{}
		for _, p := range d.Panels {
			if p.Title != "" && !seen[p.Title] {
				seen[p.Title] = true
				dash.Charts = append(dash.Charts, p.Title)
			}
		}
		sort.Strings(dash.Charts)
		out.Dashboards = append(out.Dashboards, dash)
	}
	sort.Slice(out.Dashboards, func(i, j int) bool { return out.Dashboards[i].Name < out.Dashboards[j].Name })
	return out, nil
}
