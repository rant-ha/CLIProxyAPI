package requestlog

import _ "embed"

//go:embed dashboard.html
var dashboardHTML []byte

// DashboardHTML returns the embedded dashboard page.
func DashboardHTML() []byte { return dashboardHTML }
