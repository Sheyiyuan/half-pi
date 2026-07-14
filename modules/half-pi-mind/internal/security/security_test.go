package security

import (
	"testing"
)

func TestCheckBlacklist(t *testing.T) {
	p := New()
	p.Mode = ModeYOLO // YOLO 下黑名单也生效

	tests := []struct {
		cmd      string
		wantDeny bool
	}{
		{"rm -rf /", true},
		{"rm -rf /*", true},
		{"mkfs.ext4 /dev/sda", true},
		{"dd if=/dev/zero of=/dev/sda bs=512 count=1", true},
		{":(){ :|:& };:", true},
		{"chmod -R 000 /", true},
		{"wget -O - | sh", true},
		{"curl | sh", true},
		{"curl | sh", true},
		{"shutdown now", true},
		{"reboot", true},
		{"poweroff", true},
		{"> /dev/sda", true},
		{"mv /* /dev/null", true},
		{"init 0", true},
		// Safe commands
		{"ls -la", false},
		{"echo hello", false},
		{"cat file.txt", false},
		{"", false},
	}

	for _, tt := range tests {
		decision, _ := p.Check(tt.cmd)
		got := decision == Deny
		if got != tt.wantDeny {
			t.Errorf("Check(%q) deny=%v, want %v", tt.cmd, got, tt.wantDeny)
		}
	}
}

func TestCheckModeStrict(t *testing.T) {
	p := New()
	p.Mode = ModeStrict

	// Strict: no whitelist set, almost everything needs approval
	decision, reason := p.Check("ls -la")
	if decision != NeedApproval {
		t.Errorf("strict+ls: got %v, want NeedApproval", decision)
	}
	if reason == "" {
		t.Error("strict should give a reason")
	}

	// Strict: anything works with a whitelist
	p.whitelist = []rule{
		{pattern: "ls", desc: "list"},
		{pattern: "echo", desc: "print"},
	}
	decision, _ = p.Check("ls -la")
	if decision != Allow {
		t.Errorf("strict+whitelisted: got %v, want Allow", decision)
	}

	decision, _ = p.Check("echo hello")
	if decision != Allow {
		t.Errorf("strict+whitelisted echo: got %v, want Allow", decision)
	}

	decision, _ = p.Check("rm file")
	if decision != NeedApproval {
		t.Errorf("strict+unlisted: got %v, want NeedApproval", decision)
	}
}

func TestCheckModeYOLO(t *testing.T) {
	p := New()
	p.Mode = ModeYOLO

	decision, _ := p.Check("rm file.txt")
	if decision != Allow {
		t.Errorf("yolo+rm: got %v, want Allow", decision)
	}
	decision, _ = p.Check("echo hi")
	if decision != Allow {
		t.Errorf("yolo+echo: got %v, want Allow", decision)
	}
}

func TestCheckModeTrust(t *testing.T) {
	p := New()
	p.Mode = ModeTrust

	decision, _ := p.Check("rm file.txt")
	if decision != Allow {
		t.Errorf("trust+rm: got %v, want Allow", decision)
	}
}

func TestCheckModeNormal(t *testing.T) {
	p := New() // defaults to ModeNormal

	sensitive := []string{
		"rm file.txt", "mv a b", "echo > file", "echo >> file",
		"dd if=a of=b", "sudo ls", "apt install x", "apt-get update",
		"pip install x", "npm install x", "chmod 755 f", "chown user f",
		"kill 1234", "systemctl restart", "docker ps",
	}
	for _, cmd := range sensitive {
		decision, reason := p.Check(cmd)
		if decision != NeedApproval {
			t.Errorf("normal+sensitive(%q): got %v, want NeedApproval", cmd, decision)
		}
		if reason == "" {
			t.Errorf("normal+sensitive(%q): reason should not be empty", cmd)
		}
	}

	safe := []string{"ls -la", "echo hello", "cat file.txt", "pwd"}
	for _, cmd := range safe {
		decision, _ := p.Check(cmd)
		if decision != Allow {
			t.Errorf("normal+safe(%q): got %v, want Allow", cmd, decision)
		}
	}
}

func TestCheckCaseInsensitive(t *testing.T) {
	p := New()

	// Blacklist should catch any case
	decision, _ := p.Check("RM -RF /")
	if decision != Deny {
		t.Error("blacklist should be case-insensitive")
	}

	// Sensitive patterns should catch any case
	p.Mode = ModeNormal
	decision, _ = p.Check("RM file.txt")
	if decision != NeedApproval {
		t.Error("sensitive patterns should be case-insensitive")
	}
}

func TestCheckEmpty(t *testing.T) {
	p := New()
	decision, _ := p.Check("")
	if decision != Allow {
		t.Errorf("empty command: got %v, want Allow", decision)
	}
	decision, _ = p.Check("   ")
	if decision != Allow {
		t.Errorf("whitespace command: got %v, want Allow", decision)
	}
}

func TestSetPolicy(t *testing.T) {
	p := New()
	p.Mode = ModeStrict
	SetPolicy(p)

	decision, _ := Check("echo hello")
	if decision != NeedApproval {
		t.Errorf("after SetPolicy(strict): got %v, want NeedApproval", decision)
	}

	// Restore default
	SetPolicy(New())
}
