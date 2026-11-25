//go:build e2e

// Linked only into end-to-end builds (go build -tags e2e): lets
// mygo-automation drive the example player through MYGO_AUTOMATION.
package main

import _ "github.com/egoist/mygo/plugins/automation"
