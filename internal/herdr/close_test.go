package herdr

import (
	"strings"
	"testing"
)

func TestCloseCommand(t *testing.T) {
	t.Setenv("HERDR_BIN_PATH", "herdr")
	cmd := CloseCommand("wA")
	got := strings.Join(cmd.Args, " ")
	if !strings.Contains(got, "workspace close wA") {
		t.Fatalf("expected close command, got: %s", got)
	}
}
