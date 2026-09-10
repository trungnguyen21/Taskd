package dashboard

import (
	"os/exec"
	"testing"
)

// TestWebSmoke runs the dashboard's JavaScript.
//
// Every other test in this repository can prove the bundle is served and that
// the API answers correctly, and none of them executes a line of the code the
// browser actually runs - so a syntax error or a mistyped import would ship,
// and would show as a blank page.
//
// Node is a development convenience here, not a dependency of the product:
// there is no toolchain, no package.json and nothing to install, and an
// installation that never runs this test is unaffected. So its absence skips.
func TestWebSmoke(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; skipping the dashboard's JavaScript checks")
	}

	output, err := exec.Command(node, "web_smoke.mjs", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("the dashboard's JavaScript checks failed:\n%s", output)
	}
}
