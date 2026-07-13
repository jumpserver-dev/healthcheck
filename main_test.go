package main

import (
	"bufio"
	"strings"
	"testing"
)

func TestParseArgsCheck(t *testing.T) {
	cmd := parseArgs([]string{"check", "https://example.com/health"})

	if cmd.name != "check" {
		t.Fatalf("unexpected command: %s", cmd.name)
	}
	if cmd.target != "https://example.com/health" {
		t.Fatalf("unexpected target: %s", cmd.target)
	}
}

func TestParseArgsCurl(t *testing.T) {
	cmd := parseArgs([]string{"check", "curl", "https://example.com/file"})

	if cmd.name != "curl" {
		t.Fatalf("unexpected command: %s", cmd.name)
	}
	if cmd.target != "https://example.com/file" {
		t.Fatalf("unexpected target: %s", cmd.target)
	}
}

func TestParseArgsWgetWithOutput(t *testing.T) {
	cmd := parseArgs([]string{"check", "wget", "-O", "artifact.deb", "https://example.com/file.deb"})

	if cmd.name != "wget" {
		t.Fatalf("unexpected command: %s", cmd.name)
	}
	if cmd.target != "https://example.com/file.deb" {
		t.Fatalf("unexpected target: %s", cmd.target)
	}
	if cmd.output != "artifact.deb" {
		t.Fatalf("unexpected output: %s", cmd.output)
	}
}

func TestParseArgsPing(t *testing.T) {
	cmd := parseArgs([]string{"check", "ping", "127.0.0.1"})

	if cmd.name != "ping" {
		t.Fatalf("unexpected command: %s", cmd.name)
	}
	if cmd.target != "127.0.0.1" {
		t.Fatalf("unexpected target: %s", cmd.target)
	}
}

func TestParseArgsPS(t *testing.T) {
	cmd := parseArgs([]string{"check", "ps", "check"})

	if cmd.name != "ps" {
		t.Fatalf("unexpected command: %s", cmd.name)
	}
	if cmd.filter != "check" {
		t.Fatalf("unexpected filter: %s", cmd.filter)
	}
}

func TestParseArgsNetstat(t *testing.T) {
	cmd := parseArgs([]string{"check", "netstat", "-tulnp"})

	if cmd.name != "netstat" {
		t.Fatalf("unexpected command: %s", cmd.name)
	}
}

func TestParseArgsEdit(t *testing.T) {
	cmd := parseArgs([]string{"check", "edit", "/tmp/example.conf"})

	if cmd.name != "edit" {
		t.Fatalf("unexpected command: %s", cmd.name)
	}
	if cmd.target != "/tmp/example.conf" {
		t.Fatalf("unexpected target: %s", cmd.target)
	}
}

func TestParseArgsVi(t *testing.T) {
	cmd := parseArgs([]string{"check", "vi", "/tmp/example.conf"})

	if cmd.name != "vim" {
		t.Fatalf("unexpected command: %s", cmd.name)
	}
	if cmd.target != "/tmp/example.conf" {
		t.Fatalf("unexpected target: %s", cmd.target)
	}
}

func TestParseArgsVim(t *testing.T) {
	cmd := parseArgs([]string{"check", "vim", "/tmp/example.conf"})

	if cmd.name != "vim" {
		t.Fatalf("unexpected command: %s", cmd.name)
	}
	if cmd.target != "/tmp/example.conf" {
		t.Fatalf("unexpected target: %s", cmd.target)
	}
}

func TestDefaultDownloadPath(t *testing.T) {
	name, err := defaultDownloadPath("https://example.com/releases/check_linux_amd64.deb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if name != "check_linux_amd64.deb" {
		t.Fatalf("unexpected file name: %s", name)
	}
}

func TestParseProcNetIPv4Address(t *testing.T) {
	address, port, err := parseProcNetAddress("0100007F:1F90", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if address != "127.0.0.1" {
		t.Fatalf("unexpected address: %s", address)
	}
	if port != 8080 {
		t.Fatalf("unexpected port: %d", port)
	}
}

func TestParseProcNetIPv6Address(t *testing.T) {
	address, port, err := parseProcNetAddress("00000000000000000000000000000000:01BB", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if address != "::" {
		t.Fatalf("unexpected address: %s", address)
	}
	if port != 443 {
		t.Fatalf("unexpected port: %d", port)
	}
}

func TestReadKeyParsesSS3Arrows(t *testing.T) {
	editor := &textEditor{reader: bufio.NewReader(strings.NewReader("\x1bOB"))}

	key, err := editor.readKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != keyArrowDown {
		t.Fatalf("unexpected key: %q", key)
	}
}

func TestReadKeyParsesCSIWithModifiers(t *testing.T) {
	editor := &textEditor{reader: bufio.NewReader(strings.NewReader("\x1b[1;2B"))}

	key, err := editor.readKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != keyArrowDown {
		t.Fatalf("unexpected key: %q", key)
	}
}

func TestReadKeyLeavesFollowingRuneAfterEscape(t *testing.T) {
	editor := &textEditor{reader: bufio.NewReader(strings.NewReader("\x1bx"))}

	key, err := editor.readKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "\x1b" {
		t.Fatalf("unexpected key: %q", key)
	}

	key, err = editor.readKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "x" {
		t.Fatalf("unexpected key: %q", key)
	}
}
