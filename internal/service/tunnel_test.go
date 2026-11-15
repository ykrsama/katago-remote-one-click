package service

import (
	"strings"
	"testing"
)

func TestQuickTunnelURLPattern(t *testing.T) {
	line := "Visit https://quiet-sky-123.trycloudflare.com when ready"
	if got := quickTunnelURL.FindString(line); got != "https://quiet-sky-123.trycloudflare.com" {
		t.Fatalf("URL = %q", got)
	}
	for _, invalid := range []string{
		"http://quiet-sky.trycloudflare.com",
		"https://trycloudflare.com",
		"https://quiet_sky.trycloudflare.com",
		"https://quiet-sky.example.com",
	} {
		if got := quickTunnelURL.FindString(invalid); got != "" {
			t.Fatalf("matched invalid URL %q as %q", invalid, got)
		}
	}
}

func TestRegisteredTunnelLine(t *testing.T) {
	for _, line := range []string{
		"2026-01-01T00:00:00Z INF Registered tunnel connection connIndex=0 location=nrt01",
		"INF registered tunnel connection",
		"INF Connection 5f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f registered connIndex=0",
	} {
		if !tunnelRegistered.MatchString(line) {
			t.Fatalf("registration line was not detected: %q", line)
		}
	}
	if tunnelRegistered.MatchString("INF Unregistered tunnel connection connIndex=0") {
		t.Fatal("unregistration line was treated as a ready tunnel")
	}
}

func TestTunnelCommandQuick(t *testing.T) {
	args, environment := tunnelCommand(tunnelSpec{LocalAddress: "127.0.0.1:8765"})
	command := strings.Join(args, " ")
	if !strings.Contains(command, "--url http://127.0.0.1:8765") {
		t.Fatalf("quick tunnel args = %q", command)
	}
	if strings.Contains(command, "run") {
		t.Fatalf("quick tunnel should not run a named tunnel: %q", command)
	}
	if len(environment) != 0 {
		t.Fatalf("quick tunnel environment = %q", environment)
	}
}

func TestTunnelCommandNamedKeepsTokenOutOfArgv(t *testing.T) {
	const token = "eyJhIjoiYWNjb3VudCIsInQiOiJ0dW5uZWwifQ"
	args, environment := tunnelCommand(tunnelSpec{
		Named:    true,
		Hostname: "katago.ykrsama.org",
		Token:    token,
	})
	command := strings.Join(args, " ")
	if strings.Contains(command, token) {
		t.Fatal("tunnel token leaked into cloudflared argv")
	}
	if !strings.HasSuffix(command, " run") {
		t.Fatalf("named tunnel args = %q", command)
	}
	if len(environment) != 1 || environment[0] != "TUNNEL_TOKEN="+token {
		t.Fatalf("named tunnel environment = %q", environment)
	}
}

func TestRedactSecret(t *testing.T) {
	const token = "secret-token-value"
	line := "failed to connect with token " + token
	if got := redactSecret(line, token); strings.Contains(got, token) {
		t.Fatalf("redaction failed: %q", got)
	}
}

func TestNamedTunnelProtocolEnvironment(t *testing.T) {
	_, environment := tunnelCommand(tunnelSpec{
		Named:    true,
		Hostname: "katago.example.org",
		Token:    "token-value",
		Protocol: "http2",
	})
	joined := strings.Join(environment, " ")
	if !strings.Contains(joined, "TUNNEL_TRANSPORT_PROTOCOL=http2") {
		t.Fatalf("environment = %q", environment)
	}

	_, environment = tunnelCommand(tunnelSpec{
		Named:    true,
		Hostname: "katago.example.org",
		Token:    "token-value",
	})
	if strings.Contains(strings.Join(environment, " "), "TUNNEL_TRANSPORT_PROTOCOL") {
		t.Fatalf("auto protocol should not set the env: %q", environment)
	}
}
