package catalogue

import (
	"strconv"
	"strings"
)

// PanelTypes are the panel types a generated dashboard may use. The counter
// type is metrics_counter in AxonOps dashboard templates, although the
// provider documentation calls it "counter".
var PanelTypes = []string{"row", "line-chart", "pie_chart", "metrics_counter"}

// PanelKeys are the keys a panel may have.
var PanelKeys = []string{"title", "type", "query", "legend", "w", "h"}

func init() {
	register(Kind{
		Name:         "custom_dashboard",
		ClusterTypes: []string{"cassandra", "dse", "kafka"},
		Terraform:    "axonops_custom_dashboard",
		Identity:     "name",
		Notes: "Each panel has title, type (row | line-chart | pie_chart | metrics_counter), query (a PromQL expression, " +
			"not for rows), optional legend, and optional w and h grid size (the grid is 18 columns wide). " +
			"Do not set positions; they are laid out for you. A metrics_counter shows one number, so its query must return one series (use avg or sum). " +
			"Do not put cluster, org or type matchers in a query; AxonOps adds them. " +
			"Disk metrics: host_Disk_UsedPercent (0-100, per mountpoint) and host_Disk_Used (bytes). There is no free-space metric: free percent is 100 - avg(host_Disk_UsedPercent{mountpoint='/'}).",
		Fields: []Field{
			{Name: "name", Type: String, Required: true},
			{Name: "panels", Type: Panels, Required: true},
		},
		TerraformFields: terraformDashboard,
	})
}

// Layout constants for the dashboard grid.
const (
	GridWidth     = 18
	counterWidth  = 6
	counterHeight = 4
	chartWidth    = 9
	chartHeight   = 6
)

// RawHCL is an expression written to Terraform as is.
type RawHCL string

func terraformDashboard(f map[string]any) map[string]any {
	out := map[string]any{"name": f["name"]}
	panels, _ := f["panels"].([]any)
	x, y, rowH := 0, 0, 0
	var res []any
	for _, p := range panels {
		m, _ := p.(map[string]any)
		typ, _ := m["type"].(string)
		w, h := num(m["w"]), num(m["h"])
		if typ == "row" {
			if x > 0 {
				y += rowH
				x, rowH = 0, 0
			}
			res = append(res, map[string]any{
				"title": m["title"], "type": "row",
				"layout": map[string]any{"x": 0.0, "y": float64(y), "w": float64(GridWidth), "h": 1.0},
			})
			y++
			continue
		}
		if w == 0 {
			w, h = chartWidth, chartHeight
			if typ == "metrics_counter" {
				w, h = counterWidth, counterHeight
			}
		}
		if h == 0 {
			h = chartHeight
		}
		if x+w > GridWidth {
			y += rowH
			x, rowH = 0, 0
		}
		legend, _ := m["legend"].(string)
		query, _ := m["query"].(string)
		res = append(res, map[string]any{
			"title": m["title"], "type": typ,
			"details": RawHCL("jsonencode({ y = {}, queries = [{ query = " + hclQuote(query) + ", legend = " + hclQuote(legend) + " }] })"),
			"layout":  map[string]any{"x": float64(x), "y": float64(y), "w": float64(w), "h": float64(h)},
		})
		x += w
		rowH = max(rowH, h)
	}
	out["panels"] = res
	return out
}

func hclQuote(s string) string {
	q := strconv.Quote(s)
	q = strings.ReplaceAll(q, "${", "$${")
	return strings.ReplaceAll(q, "%{", "%%{")
}

func num(v any) int {
	f, _ := v.(float64)
	return int(f)
}
