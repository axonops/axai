package read_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/read"
)

func TestClusterReadsDashboards(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "AxonApi tok", r.Header.Get("Authorization"))
		require.Equal(t, "/api/v1/dashboardtemplate/mycompany/cassandra/prod", r.URL.Path)
		_, _ = w.Write([]byte(`{"dashboards":[{"name":"System","panels":[{"title":"Disk usage %"},{"title":"Disk usage %"},{"title":"CPU"}]}]}`))
	}))
	defer srv.Close()
	c, err := read.New(read.Config{BaseURL: srv.URL, Org: "mycompany", Token: "tok", TokenType: "AxonApi"})
	require.NoError(t, err)
	got, err := c.Cluster(context.Background(), "cassandra", "prod")
	require.NoError(t, err)
	require.Equal(t, []read.Dashboard{{Name: "System", Charts: []string{"CPU", "Disk usage %"}}}, got.Dashboards)
}

func TestClusterHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }))
	defer srv.Close()
	c, _ := read.New(read.Config{BaseURL: srv.URL, Org: "o", Token: "t"})
	_, err := c.Cluster(context.Background(), "cassandra", "prod")
	require.ErrorContains(t, err, "HTTP 403")
}

func TestNewRequiresToken(t *testing.T) {
	_, err := read.New(read.Config{Org: "o"})
	require.ErrorContains(t, err, "AXONOPS_API_TOKEN")
}
