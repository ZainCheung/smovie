//go:build e2e

package main

// Built with -tags e2e, the app can be driven from outside through
// MYGO_AUTOMATION=<socket> (see mygo's automation plugin).
import _ "github.com/egoist/mygo/plugins/automation"
