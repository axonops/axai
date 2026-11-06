package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractURLs(t *testing.T) {
	got, urls := extractURLs("send to Slack using https://hooks.slack.com/services/T0/B0/xyz. Also http://a.example/x, ok")
	require.Len(t, urls, 2)
	for name, u := range urls {
		require.Regexp(t, `^REQUEST_URL_[0-9A-F]{6}$`, name)
		require.NotContains(t, got, u)
		require.Contains(t, got, name)
	}
	require.Contains(t, got, ". Also ")
}

func TestExtractURLsIsStable(t *testing.T) {
	_, a := extractURLs("use https://x.example/hook")
	_, b := extractURLs("alert via https://x.example/hook please")
	require.Equal(t, a, b)
}

func TestExtractURLsNone(t *testing.T) {
	got, urls := extractURLs("create a chart")
	require.Equal(t, "create a chart", got)
	require.Empty(t, urls)
}
