package cmd

import "testing"

func TestSelfImproveCommandIsRegistered(t *testing.T) {
	root := NewCLI()

	cmd, _, err := root.Find([]string{"self-improve"})
	if err != nil {
		t.Fatalf("self-improve is not registered: %v", err)
	}
	if cmd.Name() != "self-improve" {
		t.Fatalf("command = %q, want self-improve", cmd.Name())
	}
	if cmd.Hidden {
		t.Fatal("self-improve must be a visible command")
	}

	for _, name := range []string{"model", "cycles", "budget", "verify", "topic", "workspace", "build", "allow-dirty", "research-only", "journal"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("self-improve is missing --%s", name)
		}
	}

	// The default gate must be the repository's own documented build check, so a
	// kept cycle can never leave the tree uncompilable.
	verify, err := cmd.Flags().GetStringArray("verify")
	if err != nil {
		t.Fatal(err)
	}
	if len(verify) == 0 || verify[0] != "go build ./..." {
		t.Fatalf("default verify = %v, want [go build ./...]", verify)
	}
}
